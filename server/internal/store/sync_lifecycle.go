package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// opDelete sets a tombstone, causally at row level.
//
// Two rules meet here:
//
//   - A stale deletion — one whose author had not seen the editor's change —
//     does NOT win. It is receipted with a notice to the person who asked for
//     it, because a deletion destroys: it asks, it does not impose.
//   - Deleting the OPEN occurrence of a series advances that series. That is
//     what makes the invariant exactly-one rather than at-most-one: "we are
//     skipping this week" cannot leave a series with nothing open. And it is
//     why there is no sweeper regenerating what somebody deleted — a sweeper
//     resurrects, every quarter of an hour, forever, the chore they just
//     dismissed.
func (s *Store) opDelete(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	shape, exists := shapeOf(op.Entity)
	if !exists || in.Protocol < shape.since {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockRow(ctx, tx, shape, householdID, op.RowID)
	if err != nil {
		return OpResult{}, err
	}
	if !found {
		return refusal(op.OpID, ReasonTargetGone), nil
	}
	if target.deleted {
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed, Rev: target.rev}, nil
	}

	if op.BaseRev != nil && *op.BaseRev < target.rev {
		// Somebody edited it after this author last looked. Their work stands,
		// and the person who asked for the deletion is told so.
		return s.noticeOnly(ctx, tx, householdID, in, op, noticeSpec{
			kind:   "decision",
			code:   ReasonStaleDelete,
			mine:   map[string]any{"base_rev": *op.BaseRev},
			theirs: map[string]any{"rev": target.rev},
		}, target.rev)
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	// The content columns are KEPT. A tombstone is a column, not a table:
	// SELECT * on the row still tells the whole story, including what the
	// thing was called.
	//
	//nolint:gosec // the table comes from shapeOf's closed switch
	bury := "UPDATE " + shape.table +
		" SET deleted_rev = $3, rev = $3, updated_by = $4, updated_at = now()" +
		" WHERE household_id = $1 AND id = $2"
	if _, err := tx.Exec(ctx, bury, householdID, op.RowID, seq, in.MemberID); err != nil {
		return OpResult{}, fmt.Errorf("store: burying a %s: %w", shape.entity, err)
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

// opRestore clears a tombstone, through the successor rule.
//
// Restoring a buried occurrence would re-open one the series has moved past, so
// it goes through the same gate as un-ticking: cancel an untouched successor,
// or refuse with something a person can act on.
func (s *Store) opRestore(
	ctx context.Context, tx pgx.Tx, householdID uuid.UUID, in SyncInput, op Op,
) (OpResult, error) {
	shape, exists := shapeOf(op.Entity)
	if !exists || in.Protocol < shape.since {
		return refusal(op.OpID, ReasonUnsupportedOp), nil
	}
	if op.RowID == uuid.Nil {
		return refusal(op.OpID, ReasonMalformed), nil
	}

	target, found, err := s.lockRow(ctx, tx, shape, householdID, op.RowID)
	if err != nil {
		return OpResult{}, err
	}
	if !found {
		return refusal(op.OpID, ReasonTargetGone), nil
	}
	if !target.deleted {
		return OpResult{OpID: op.OpID, Outcome: OutcomeAbsorbed, Rev: target.rev}, nil
	}

	seq, err := s.nextSeq(ctx, tx, householdID)
	if err != nil {
		return OpResult{}, err
	}

	if target.seriesID != nil && target.seriesIndex != nil {
		allowed, err := s.reopenSeries(ctx, tx, householdID,
			*target.seriesID, op.RowID, *target.seriesIndex, in.MemberID, seq)
		if err != nil {
			return OpResult{}, err
		}
		if !allowed {
			// Same as un-ticking: refused, but with something a person can act
			// on rather than a dead end.
			return s.refuseAndTell(
				ctx, tx, householdID, in, op, ReasonSuccessorInUse, nil)
		}
	}

	//nolint:gosec // the table comes from shapeOf's closed switch
	raise := "UPDATE " + shape.table +
		" SET deleted_rev = NULL, rev = $3, updated_by = $4, updated_at = now()" +
		" WHERE household_id = $1 AND id = $2"
	if _, err := tx.Exec(ctx, raise, householdID, op.RowID, seq, in.MemberID); err != nil {
		return OpResult{}, fmt.Errorf("store: restoring a %s: %w", shape.entity, err)
	}

	return OpResult{OpID: op.OpID, Outcome: OutcomeApplied, Rev: seq}, nil
}
