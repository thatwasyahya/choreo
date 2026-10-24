package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// scheduled creates a series with a real schedule and its first occurrence.
//
// Seeded with SQL because creating a series is still a Phase 2 operation; the
// arithmetic it drives is what these tests are about.
func (w *world) scheduled(
	title, mode, rule string, relDays *int, anchor time.Time,
	catchUp string, rotation []uuid.UUID,
) (seriesID, first uuid.UUID) {
	w.t.Helper()

	seriesID = uuid.New()
	first = store.OccurrenceID(seriesID, 0)

	// An empty array, not NULL: a nil slice crosses as NULL and the column
	// refuses it. "Nobody in particular" is a rotation of nobody.
	if rotation == nil {
		rotation = []uuid.UUID{}
	}

	execSQL(w.t, `INSERT INTO series
	    (id, household_id, rev, created_rev, updated_by, title, mode, rule,
	     rel_days, anchor_on, catch_up, rotation, next_index)
	    VALUES ($1, $2, 2, 2, $3, $4, $5, $6, $7, $8, $9, $10, 0)`,
		seriesID, w.id, w.member, title, mode, rule, relDays, anchor,
		catchUp, rotation)

	execSQL(w.t, `INSERT INTO tasks
	    (id, household_id, rev, created_rev, updated_by, kind, title,
	     series_id, series_index, due_on)
	    VALUES ($1, $2, 3, 3, $3, 'task', $4, $5, 0, $6)`,
		first, w.id, w.member, title, seriesID, anchor)

	execSQL(w.t, `UPDATE series SET open_occurrence_id = $3
	               WHERE household_id = $1 AND id = $2`, w.id, seriesID, first)

	return seriesID, first
}

func (w *world) dueOn(task uuid.UUID) *time.Time {
	w.t.Helper()

	var due *time.Time
	scanRow(w.t, &due, `SELECT due_on FROM tasks
	                     WHERE household_id = $1 AND id = $2`, w.id, task)
	return due
}

func (w *world) assignee(task uuid.UUID) *uuid.UUID {
	w.t.Helper()

	var who *uuid.UUID
	scanRow(w.t, &who, `SELECT assignee_id FROM tasks
	                     WHERE household_id = $1 AND id = $2`, w.id, task)
	return who
}

// Ticking a weekly chore produces the next one, dated.
//
// Phase 1 owed the invariant — exactly one open occurrence — and left the new
// one without a date. This is the arithmetic that fills it in.
func TestTickingAWeeklyChoreDatesTheNextOne(t *testing.T) {
	w := newWorld(t)

	anchor := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) // a Tuesday
	series, first := w.scheduled(
		"Sortir les poubelles", "calendar", "FREQ=WEEKLY;BYDAY=TU",
		nil, anchor, "skip", nil,
	)

	w.apply(store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: "task",
		RowID: first, CompletionID: uuid.New(),
	})

	next := store.OccurrenceID(series, 1)
	due := w.dueOn(next)
	if due == nil {
		t.Fatal("the next occurrence has no date: the schedule did not run")
	}
	if got := due.Weekday(); got != time.Tuesday {
		t.Errorf("the next bin day is a %s", got)
	}
	if due.Before(time.Now().UTC().AddDate(0, 0, -1)) {
		t.Errorf("the next occurrence is dated %s, in the past: catch_up skip "+
			"should have walked it forward", due.Format("2006-01-02"))
	}
}

// A relative series counts from the TICK, which is the whole reason the mode
// exists: "three weeks after the last time", not three weeks after a date
// nobody honoured.
func TestARelativeSeriesCountsFromTheTick(t *testing.T) {
	w := newWorld(t)

	anchor := time.Now().UTC().AddDate(0, 0, -40)
	series, first := w.scheduled(
		"Changer les draps", "relative", "", intPtr(21), anchor, "skip", nil,
	)

	w.apply(store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: "task",
		RowID: first, CompletionID: uuid.New(),
	})

	due := w.dueOn(store.OccurrenceID(series, 1))
	if due == nil {
		t.Fatal("no date on the next occurrence")
	}

	// Ticked today, so three weeks from today — not from the anchor forty days
	// ago, which would already be in the past.
	want := time.Now().UTC().AddDate(0, 0, 21)
	if gap := due.Sub(want); gap > 36*time.Hour || gap < -36*time.Hour {
		t.Errorf("due %s, want about %s: counted from the prediction rather "+
			"than from the tick", due.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// Whose turn it is walks with the index, so two offline phones compute the
// same answer rather than the one that synchronised first winning.
func TestTheRotationWalksWithTheIndex(t *testing.T) {
	w := newWorld(t)

	sara := w.addMember("Sara")
	rotation := []uuid.UUID{w.member, sara}

	anchor := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	series, first := w.scheduled(
		"Vaisselle", "calendar", "FREQ=DAILY", nil, anchor, "skip", rotation,
	)

	w.apply(store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: "task",
		RowID: first, CompletionID: uuid.New(),
	})

	second := store.OccurrenceID(series, 1)
	if who := w.assignee(second); who == nil || *who != sara {
		t.Errorf("occurrence 1 assigned to %v, want Sara: index 1 of a "+
			"two-person rotation", who)
	}

	w.apply(store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: "task",
		RowID: second, CompletionID: uuid.New(),
	})

	third := store.OccurrenceID(series, 2)
	if who := w.assignee(third); who == nil || *who != w.member {
		t.Errorf("occurrence 2 assigned to %v, want the first member back", who)
	}
}

// A rule nobody can parse must never destroy a tick. The occurrence arrives
// without a date — visible, tickable, and obviously wrong to whoever set it
// up — rather than the whole transaction failing.
func TestAnUnreadableRuleStillLetsSomebodyTickTheChore(t *testing.T) {
	w := newWorld(t)

	anchor := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	series, first := w.scheduled(
		"Arroser", "calendar", "FREQ=FORTNIGHTLY_ISH", nil, anchor, "skip", nil,
	)

	out := w.apply(store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: "task",
		RowID: first, CompletionID: uuid.New(),
	})

	if got := out.Results[0].Outcome; got != store.OutcomeApplied {
		t.Fatalf("the tick came back %q: a bad rule must not cost somebody "+
			"their gesture", got)
	}
	if due := w.dueOn(store.OccurrenceID(series, 1)); due != nil {
		t.Errorf("due %s, want no date at all", due.Format("2006-01-02"))
	}
}

func intPtr(v int) *int { return &v }
