package httpapi_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// repeats queues the operation a phone sends when somebody sets up a chore
// that comes back.
func (p *phone) repeats(
	title, rule string, startsOn time.Time, rotation ...string,
) string {
	p.t.Helper()

	id := uuid.NewString()
	fields := map[string]any{
		"title":     title,
		"mode":      "calendar",
		"rule":      rule,
		"anchor_on": startsOn.Format("2006-01-02"),
		"catch_up":  "skip",
	}
	if len(rotation) > 0 {
		fields["rotation"] = rotation
	}

	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "create", "entity": "series",
		"row_id": id, "fields": fields,
	})
	return id
}

// / seriesRow is what this phone knows about a series.
func (p *phone) seriesRow(id string) map[string]any {
	p.t.Helper()
	return p.rows[id].state
}

// A recurring chore, end to end, over HTTP and through the database.
//
// The unit tests say the arithmetic is right. This says the path is: the
// operation a phone actually queues, the row it actually gets back, and the
// occurrence that appears without anybody asking for it.
func TestARecurringChoreGoesRoundOverHTTP(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")

	// Set up on a Tuesday, repeating every Tuesday.
	tuesday := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	series := camille.repeats("Sortir les poubelles", "FREQ=WEEKLY;BYDAY=TU", tuesday)

	if outcome := str(only(t, camille.sync()), "outcome"); outcome != "applied" {
		t.Fatalf("creating the series: %q", outcome)
	}

	// The series and its first occurrence both came down in the same answer:
	// the phone did not have to guess either of them.
	if title := str(camille.seriesRow(series), "title"); title != "Sortir les poubelles" {
		t.Errorf("the series did not come back: %v", camille.seriesRow(series))
	}

	first := occurrence(t, camille, series)
	if due := str(first, "due_on"); !hasDate(due, "2026-10-27") {
		t.Errorf("the first occurrence is due %q, want the day it starts", due)
	}

	// Somebody takes the bins out.
	camille.ticks(rowOf(t, camille, series, 0))
	if outcome := str(only(t, camille.sync()), "outcome"); outcome != "applied" {
		t.Fatal("the tick was refused")
	}

	// And next week's is already there, dated, without anybody asking.
	camille.pull()
	next := occurrence(t, camille, series)
	due := str(next, "due_on")
	if !hasDate(due, "2026-11-03") {
		t.Errorf("the next bin day is %q, want the following Tuesday", due)
	}
	if str(next, "title") != "Sortir les poubelles" {
		t.Error("the new occurrence lost the chore's name")
	}
}

// Whose turn it is comes down with the row, so both phones show the same name
// without computing anything.
func TestTheRotationIsVisibleToBothPhones(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	series := camille.repeats(
		"Vaisselle", "FREQ=DAILY",
		time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		camille.member, jordan.member,
	)
	camille.sync()

	// Occurrence zero goes to the first in the rotation.
	first := occurrence(t, camille, series)
	if who := str(first, "assignee_id"); who != camille.member {
		t.Errorf("occurrence 0 assigned to %q, want the first in the rotation", who)
	}

	camille.ticks(rowOf(t, camille, series, 0))
	camille.sync()
	jordan.pull()

	// And the next one to the second — on Jordan's phone, which computed
	// nothing: it read the row.
	second := jordanOccurrence(t, jordan, series)
	if who := str(second, "assignee_id"); who != jordan.member {
		t.Errorf("occurrence 1 assigned to %q, want Jordan's turn", who)
	}
}

// --- small readers ----------------------------------------------------------

// occurrence returns the open occurrence of a series, as this phone sees it.
func occurrence(t *testing.T, p *phone, series string) map[string]any {
	t.Helper()

	open := str(p.seriesRow(series), "open_occurrence_id")
	if open == "" {
		t.Fatalf("the series has no open occurrence: %v", p.seriesRow(series))
	}
	row, present := p.rows[open]
	if !present {
		t.Fatalf("the open occurrence %s never reached this phone", open)
	}
	return row.state
}

func jordanOccurrence(t *testing.T, p *phone, series string) map[string]any {
	t.Helper()
	return occurrence(t, p, series)
}

func rowOf(t *testing.T, p *phone, series string, index int) string {
	t.Helper()

	for id, row := range p.rows {
		if str(row.state, "series_id") != series {
			continue
		}
		if int(num(row.state, "series_index")) == index {
			return id
		}
	}
	t.Fatalf("occurrence %d of %s is not on this phone", index, series)
	return ""
}

// hasDate compares a date the server rendered — which may carry a time and a
// zone — against the civil day that was meant.
func hasDate(rendered, want string) bool {
	return len(rendered) >= len(want) && rendered[:len(want)] == want
}
