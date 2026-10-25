package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// rowFacts is the little a lifecycle operation needs to know about its target.
type rowFacts struct {
	rev         int64
	deleted     bool
	seriesID    *uuid.UUID
	seriesIndex *int
}

// lockRow takes the row lock and reads what the arbiter needs.
//
// The two occurrence columns come back as typed nulls for the tables that do
// not have them, so one scan serves every entity. Being an occurrence of a
// recurring chore is a fact about tasks, not about rows in general — and a
// list item that pretended to have a series would quietly grow one.
func (s *Store) lockRow(
	ctx context.Context, tx pgx.Tx, shape rowShape, householdID, rowID uuid.UUID,
) (rowFacts, bool, error) {
	occurrence := "NULL::uuid, NULL::int"
	if shape.series {
		occurrence = "series_id, series_index"
	}

	//nolint:gosec // table and columns come from shapeOf's closed switch
	q := "SELECT rev, deleted_rev IS NOT NULL, " + occurrence +
		" FROM " + shape.table +
		" WHERE household_id = $1 AND id = $2 FOR UPDATE"

	var row rowFacts
	err := tx.QueryRow(ctx, q, householdID, rowID).
		Scan(&row.rev, &row.deleted, &row.seriesID, &row.seriesIndex)
	switch {
	case errorsIsNoRows(err):
		return rowFacts{}, false, nil
	case err != nil:
		return rowFacts{}, false, fmt.Errorf("store: locking a %s: %w", shape.entity, err)
	}
	return row, true, nil
}

// opComplete records a tick.
//
// Additive, and NEVER refused. Completions are a set indexed by member, not a
// boolean: two adults ticking the same chore are not in conflict, so there is
// nothing to resolve — the conflict is simply never created.
//
// A completion is also never destroyed by a constraint failure downstream. If
// the series cannot advance, the tick still stands and the answer merely says
// advanced: false.
func (s *Store) opComplete(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	shape, exists := shapeOf(op.Entity)
	if !exists || in.Protocol < shape.since {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	// Ticking something that cannot be ticked is malformed and not
	// unsupported: unsupported invites a retry, and no deploy will ever make a
	// ticked list mean anything.
	if !shape.tickable || op.RowID == uuid.Nil || op.CompletionID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockRow(ctx, tx, shape, householdID, op.RowID)
	if err != nil {
		return OpResult{}, err
	}
	// A tick is work somebody actually did. If the server cannot record it, the
	// one thing it must not do is stay silent about it.
	if !found {
		return s.refuseAndTell(ctx, tx, householdID, in, op, ReasonTargetGone, nil)
	}
	if target.deleted {
		// The row is gone from the display; recording a tick against it would
		// be recording work on something nobody can see.
		return s.refuseAndTell(ctx, tx, householdID, in, op, ReasonTargetGone, nil)
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	// occurred_on is proposed by the client and bounded here, against the
	// SERVER's date. A handset set to 2031 cannot date a chore in the future,
	// and one set to 1997 cannot bury it in the past; either way the proposal
	// is remembered as suspect rather than silently believed.
	const record = `
	WITH bounded AS (
	    -- make_interval rather than ($7 || ' days')::interval: concatenating
	    -- with a text literal makes PostgreSQL infer the parameter as text, and
	    -- then an integer cannot be encoded into it at all.
	    SELECT LEAST(GREATEST(COALESCE($6::date, current_date),
	                          (current_date - make_interval(days => $7::int))::date),
	                 current_date)::date AS day
	)
	INSERT INTO completions
	    (household_id, row_id, member_id, completion_id, done_rev,
	     occurred_on, client_ts, clock_suspect)
	SELECT $1, $2, $3, $4, $5, b.day, $8,
	       COALESCE($6::date, current_date) <> b.day
	  FROM bounded b
	ON CONFLICT (household_id, row_id, member_id) DO NOTHING`

	var proposed *string
	if op.OccurredOn != "" {
		proposed = &op.OccurredOn
	}

	tag, err := tx.Exec(ctx, record,
		householdID, op.RowID, in.MemberID, op.CompletionID, seq,
		proposed, occurredOnWindow, op.ClientTS)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: recording a completion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// This member had already ticked it. The completion_id is the
		// deduplication key for points, so counting it twice is the one thing
		// to avoid.
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed, Rev: target.rev}, nil
	}

	// The revision moves whatever the row is, or the tick never reaches the
	// other phone. Only a task ALSO remembers being closed: an item on a
	// shopping list is closed by its completion and by nothing else, and two
	// columns that can disagree are two columns that will.
	closed := ""
	if shape.closes {
		closed = "closed_rev = $3, "
	}

	//nolint:gosec // table and columns come from shapeOf's closed switch
	close := "UPDATE " + shape.table + " SET " + closed +
		"rev = $3, updated_by = $4, updated_at = now()" +
		" WHERE household_id = $1 AND id = $2"
	if _, err := tx.Exec(ctx, close, householdID, op.RowID, seq, in.MemberID); err != nil {
		return OpResult{}, fmt.Errorf("store: closing a %s: %w", shape.entity, err)
	}

	result := OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}

	if target.seriesID != nil {
		advanced, err := s.advanceSeries(
			ctx, tx, householdID, *target.seriesID, op.RowID, in.MemberID, seq)
		if err != nil {
			return OpResult{}, err
		}
		result.Fields = map[string]string{"series_advanced": boolWord(advanced)}
	}

	return result, nil
}

func boolWord(flag bool) string {
	if flag {
		return "true"
	}
	return "false"
}

// opUncomplete removes exactly the completions the author had in front of them.
//
// Not "all of them": unticking in the kitchen must not erase a completion
// recorded in the garage ten seconds earlier. Removal by observation is what
// makes the gesture mean what the person intended.
func (s *Store) opUncomplete(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	shape, exists := shapeOf(op.Entity)
	if !exists || in.Protocol < shape.since {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if !shape.tickable || op.RowID == uuid.Nil || len(op.Observed) == 0 {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockRow(ctx, tx, shape, householdID, op.RowID)
	if err != nil {
		return OpResult{}, err
	}
	if !found {
		return refusal(op.OpID, ReasonTargetGone), nil
	}

	// Would this empty the set? Asked BEFORE anything is deleted, because the
	// successor rule may refuse — and a refusal must not leave a deletion
	// behind it.
	const remaining = `SELECT count(*) FROM completions
	                    WHERE household_id = $1 AND row_id = $2
	                      AND member_id <> ALL($3::uuid[])`

	var left int
	if err := tx.QueryRow(ctx, remaining, householdID, op.RowID, op.Observed).Scan(&left); err != nil {
		return OpResult{}, fmt.Errorf("store: counting completions: %w", err)
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	// Re-opening only arises when the set empties: an occurrence with a
	// completion left on it is still closed.
	if left == 0 && target.seriesID != nil && target.seriesIndex != nil {
		allowed, err := s.reopenSeries(ctx, tx, householdID,
			*target.seriesID, op.RowID, *target.seriesIndex, in.MemberID, seq)
		if err != nil {
			return OpResult{}, err
		}
		if !allowed {
			// Refusing an UNtick is acceptable where refusing a tick never is:
			// unticking destroys a fact the whole household can see, whereas
			// ticking only ever adds.
			//
			// But it is refused WITH a sentence a person can act on, never a
			// silent no: "cannot go back, the next occurrence has already been
			// changed".
			return s.refuseAndTell(
				ctx, tx, householdID, in, op, ReasonSuccessorInUse, nil)
		}
	}

	const remove = `DELETE FROM completions
	                 WHERE household_id = $1 AND row_id = $2
	                   AND member_id = ANY($3::uuid[])`
	tag, err := tx.Exec(ctx, remove, householdID, op.RowID, op.Observed)
	if err != nil {
		return OpResult{}, fmt.Errorf("store: removing completions: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed, Rev: target.rev}, nil
	}

	args := []any{householdID, op.RowID, seq, in.MemberID}
	clear := ""
	if shape.closes {
		// A row with a completion left on it is still closed, so the column
		// only clears when the set empties.
		clear = "closed_rev = CASE WHEN $5 = 0 THEN NULL ELSE closed_rev END, "
		args = append(args, left)
	}

	//nolint:gosec // table and columns come from shapeOf's closed switch
	reopen := "UPDATE " + shape.table + " SET " + clear +
		"rev = $3, updated_by = $4, updated_at = now()" +
		" WHERE household_id = $1 AND id = $2"
	if _, err := tx.Exec(ctx, reopen, args...); err != nil {
		return OpResult{}, fmt.Errorf("store: reopening a %s: %w", shape.entity, err)
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}
