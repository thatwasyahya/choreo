package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// taskRow is the little a lifecycle operation needs to know about its target.
type taskRow struct {
	rev         int64
	deleted     bool
	seriesID    *uuid.UUID
	seriesIndex *int
}

// lockTask takes the row lock and reads what the arbiter needs.
func (s *Store) lockTask(
	ctx context.Context, tx pgx.Tx, householdID, rowID uuid.UUID,
) (taskRow, bool, error) {
	const q = `SELECT rev, deleted_rev IS NOT NULL, series_id, series_index
	             FROM tasks
	            WHERE household_id = $1 AND id = $2
	              FOR UPDATE`

	var row taskRow
	err := tx.QueryRow(ctx, q, householdID, rowID).
		Scan(&row.rev, &row.deleted, &row.seriesID, &row.seriesIndex)
	switch {
	case errorsIsNoRows(err):
		return taskRow{}, false, nil
	case err != nil:
		return taskRow{}, false, fmt.Errorf("store: locking a task: %w", err)
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
	if op.Entity != EntityTask || op.RowID == uuid.Nil || op.CompletionID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockTask(ctx, tx, householdID, op.RowID)
	if err != nil {
		return OpResult{}, err
	}
	if !found {
		return refusal(op.OpID, ReasonTargetGone), nil
	}
	if target.deleted {
		// The row is gone from the display; recording a tick against it would
		// be recording work on something nobody can see.
		return refusal(op.OpID, ReasonTargetGone), nil
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

	const close = `UPDATE tasks
	                  SET closed_rev = $3, rev = $3, updated_by = $4, updated_at = now()
	                WHERE household_id = $1 AND id = $2`
	if _, err := tx.Exec(ctx, close, householdID, op.RowID, seq, in.MemberID); err != nil {
		return OpResult{}, fmt.Errorf("store: closing a task: %w", err)
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
	if op.Entity != EntityTask || op.RowID == uuid.Nil || len(op.Observed) == 0 {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockTask(ctx, tx, householdID, op.RowID)
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
			return refusal(op.OpID, ReasonSuccessorInUse), nil
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

	const reopen = `UPDATE tasks
	                   SET closed_rev = CASE WHEN $5 = 0 THEN NULL ELSE closed_rev END,
	                       rev = $3, updated_by = $4, updated_at = now()
	                 WHERE household_id = $1 AND id = $2`
	if _, err := tx.Exec(ctx, reopen,
		householdID, op.RowID, seq, in.MemberID, left); err != nil {
		return OpResult{}, fmt.Errorf("store: reopening a task: %w", err)
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}
