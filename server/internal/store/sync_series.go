package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// occurrenceNamespace seeds the derived identity of every series occurrence.
// A fixed value: changing it would re-derive every id and break the invariant
// for every existing household.
const occurrenceNamespace = "b7f4b0d2-5c3a-4e1f-9a6d-2e8c1f0a7b35"

// OccurrenceID derives the id of one occurrence of a series.
//
// From an INTEGER index, never from a computed date. Two offline phones opening
// the next occurrence therefore compute the SAME id, and the second insert
// becomes ON CONFLICT DO NOTHING — there is nothing to merge and nothing to
// arbitrate.
//
// Deriving it from a date instead would break the invariant during a rolling
// deploy: a tzdata skew between an N-1 app and the server would mint two
// different ids for the same occurrence, in some households only, at the worst
// possible moment. Integer addition has no versions.
func OccurrenceID(seriesID uuid.UUID, index int) uuid.UUID {
	return uuid.NewSHA1(uuid.MustParse(occurrenceNamespace),
		[]byte(seriesID.String()+":"+strconv.Itoa(index)))
}

// advanceSeries moves a series on, if the gesture really targeted its open
// occurrence.
//
// Zero rows updated is NOT an error. Nothing is rolled back and nothing fails:
// the human gesture that triggered this — a tick, usually — must never be
// destroyed by a constraint failure. Putting the occurrence creation and the
// completion in one unguarded transaction is how a finished chore disappears
// without a word.
func (s *Store) advanceSeries(
	ctx context.Context, tx pgx.Tx,
	householdID, seriesID, expected uuid.UUID, by uuid.UUID, seq int64,
) (bool, error) {
	const cas = `UPDATE series
	                SET next_index = next_index + 1,
	                    open_occurrence_id = NULL,
	                    rev = $4,
	                    updated_by = $5,
	                    updated_at = now()
	              WHERE household_id = $1 AND id = $2
	                AND open_occurrence_id = $3
	                AND paused = false AND deleted_rev IS NULL
	          RETURNING next_index, title`

	var (
		nextIndex int
		title     string
	)
	err := tx.QueryRow(ctx, cas, householdID, seriesID, expected, seq, by).
		Scan(&nextIndex, &title)
	switch {
	case errorsIsNoRows(err):
		// Somebody else already advanced it, or it is paused or deleted. The
		// caller keeps whatever it did and reports advanced: false.
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: advancing a series: %w", err)
	}

	next := OccurrenceID(seriesID, nextIndex)

	// The schedule arithmetic — RRULE subset, "X days after the last time",
	// rotation — belongs to phase 2. Phase 1 owes the INVARIANT: exactly one
	// open occurrence, with a derived identity. So the next occurrence is
	// created with no date, and phase 2 fills it in.
	const create = `INSERT INTO tasks
	    (id, household_id, rev, created_rev, updated_by, kind, title,
	     series_id, series_index)
	    VALUES ($1, $2, $3, $3, $4, 'task', $5, $6, $7)
	    ON CONFLICT (household_id, id) DO NOTHING`

	if _, err := tx.Exec(ctx, create,
		next, householdID, seq, by, title, seriesID, nextIndex); err != nil {
		return false, fmt.Errorf("store: creating the next occurrence: %w", err)
	}

	const point = `UPDATE series SET open_occurrence_id = $3
	                WHERE household_id = $1 AND id = $2`
	if _, err := tx.Exec(ctx, point, householdID, seriesID, next); err != nil {
		return false, fmt.Errorf("store: pointing at the next occurrence: %w", err)
	}

	return true, nil
}

// reopenSeries applies the successor rule, for the two gestures that would
// destroy a visible fact: un-ticking an occurrence, and restoring a deleted one.
//
// Three outcomes, in order:
//
//  1. the target IS the open occurrence — nothing to arbitrate, go ahead;
//  2. the successor is UNTOUCHED (rev == created_rev, no overrides, no
//     completions) — it is cancelled, the index steps back, and the register
//     points at the target again. That is exactly what someone means by "I made
//     a mistake";
//  3. otherwise — refused, because going back would discard work already done.
func (s *Store) reopenSeries(
	ctx context.Context, tx pgx.Tx,
	householdID, seriesID, target uuid.UUID, index int, by uuid.UUID, seq int64,
) (bool, error) {
	const current = `SELECT open_occurrence_id, next_index
	                   FROM series
	                  WHERE household_id = $1 AND id = $2
	                    AND deleted_rev IS NULL
	                    FOR UPDATE`

	var (
		open      *uuid.UUID
		nextIndex int
	)
	err := tx.QueryRow(ctx, current, householdID, seriesID).Scan(&open, &nextIndex)
	switch {
	case errorsIsNoRows(err):
		// No series to arbitrate against: the occurrence stands alone.
		return true, nil
	case err != nil:
		return false, fmt.Errorf("store: reading a series: %w", err)
	}

	if open != nil && *open == target {
		return true, nil
	}

	successor := OccurrenceID(seriesID, index+1)

	const untouched = `SELECT rev = created_rev
	                          AND cardinality(overrides) = 0
	                          AND NOT EXISTS (SELECT 1 FROM completions c
	                                           WHERE c.household_id = t.household_id
	                                             AND c.row_id = t.id)
	                     FROM tasks t
	                    WHERE t.household_id = $1 AND t.id = $2
	                      AND t.deleted_rev IS NULL
	                      FOR UPDATE`

	var intact bool
	err = tx.QueryRow(ctx, untouched, householdID, successor).Scan(&intact)
	switch {
	case errorsIsNoRows(err):
		// There is no successor to cancel, so nothing stands in the way.
		intact = true
	case err != nil:
		return false, fmt.Errorf("store: inspecting a successor: %w", err)
	}

	if !intact {
		return false, nil
	}

	// A hard delete and not a tombstone: this row was generated, never seen,
	// and never touched. Leaving a tombstone would mean synchronising the
	// ghost of a mistake to every phone in the household.
	const cancel = `DELETE FROM tasks
	                 WHERE household_id = $1 AND id = $2
	                   AND rev = created_rev`
	if _, err := tx.Exec(ctx, cancel, householdID, successor); err != nil {
		return false, fmt.Errorf("store: cancelling a successor: %w", err)
	}

	const step = `UPDATE series
	                 SET next_index = $3,
	                     open_occurrence_id = $4,
	                     rev = $5,
	                     updated_by = $6,
	                     updated_at = now()
	               WHERE household_id = $1 AND id = $2`
	if _, err := tx.Exec(ctx, step,
		householdID, seriesID, index, target, seq, by); err != nil {
		return false, fmt.Errorf("store: stepping a series back: %w", err)
	}

	return true, nil
}
