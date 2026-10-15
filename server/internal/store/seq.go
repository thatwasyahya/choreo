package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// nextSeq increments and returns the household's change sequence.
//
// This is the clock of the whole system, and the single most load-bearing query
// in the server. Three things about it are not negotiable:
//
//   - It runs INSIDE the caller's transaction. The sequence number and the rows
//     that carry it as their rev must become visible together, or a client could
//     advance its cursor past a row it never received.
//   - The UPDATE takes a row lock on the household, which serialises every
//     writer for that household. That is the point: assignment order becomes
//     commit order, so "everything after 48210" cannot miss anything.
//   - It is deliberately NOT a PostgreSQL sequence. Those are fast precisely
//     because they do not lock — which means they hand out numbers that commit
//     out of order and leave gaps. Either one breaks a cursor.
//
// The cost is that writes to one household are serial. A household produces a
// few dozen writes a day, so the contention is theoretical; the correctness is
// not.
func (s *Store) nextSeq(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) (int64, error) {
	const q = `UPDATE households
	              SET change_seq = change_seq + 1
	            WHERE id = $1
	        RETURNING change_seq`

	var seq int64
	if err := tx.QueryRow(ctx, q, householdID).Scan(&seq); err != nil {
		if errorsIsNoRows(err) {
			return 0, ErrHouseholdUnknown
		}
		return 0, fmt.Errorf("store: bumping the household sequence: %w", err)
	}
	return seq, nil
}

// withTx runs fn in a transaction, committing on success and rolling back on
// any error or panic.
//
// Every write path goes through here. A write that bumps the sequence outside a
// transaction would be a torn change: the number issued, the row missing.
func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: beginning a transaction: %w", err)
	}

	defer func() {
		// Rollback after a successful commit is a no-op, so this is safe
		// unconditionally and also covers a panic.
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: committing: %w", err)
	}
	return nil
}
