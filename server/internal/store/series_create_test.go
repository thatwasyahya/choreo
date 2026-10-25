package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// createSeries sends the operation a client queues when somebody makes a
// recurring chore, at the protocol version that knows about them.
func (w *world) createSeries(fields map[string]any) (uuid.UUID, store.OpResult) {
	w.t.Helper()

	id := uuid.New()
	out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "2.0.0",
		Protocol: 2,
		Ops: []store.Op{{
			OpID: uuid.New(), Kind: store.OpCreate,
			Entity: store.EntitySeries, RowID: id, Fields: fields,
		}},
	})
	if err != nil {
		w.t.Fatalf("ApplySync: %v", err)
	}
	return id, out.Results[0]
}

func weekly(extra map[string]any) map[string]any {
	fields := map[string]any{
		"title":     "Sortir les poubelles",
		"mode":      "calendar",
		"rule":      "FREQ=WEEKLY;BYDAY=TU",
		"anchor_on": "2026-10-27",
		"catch_up":  "skip",
	}
	for name, value := range extra {
		fields[name] = value
	}
	return fields
}

// A series is born with its first occurrence open. Both, or neither: a series
// with nothing open is a chore that exists and never appears, and repairing
// that later means something, somewhere, sweeping — which this design does not
// have and does not want.
func TestCreatingASeriesOpensItsFirstOccurrence(t *testing.T) {
	w := newWorld(t)

	series, result := w.createSeries(weekly(nil))
	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("outcome %q (%s), want applied", result.Outcome, result.Code)
	}

	first := store.OccurrenceID(series, 0)
	if open := w.openOccurrence(series); open == nil || *open != first {
		t.Errorf("open occurrence %v, want %v", open, first)
	}

	due := w.dueOn(first)
	if due == nil {
		t.Fatal("the first occurrence has no date")
	}
	if want := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); !due.Equal(want) {
		t.Errorf("due %s, want the anchor %s",
			due.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// Two phones that made the same chore agree; there is nothing to resolve.
func TestCreatingTheSameSeriesTwiceIsAbsorbed(t *testing.T) {
	w := newWorld(t)

	id := uuid.New()
	send := func() store.OpResult {
		out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
			DeviceID: uuid.New(), MemberID: w.member, AppVersion: "2.0.0",
			Protocol: 2,
			Ops: []store.Op{{
				OpID: uuid.New(), Kind: store.OpCreate,
				Entity: store.EntitySeries, RowID: id, Fields: weekly(nil),
			}},
		})
		if err != nil {
			t.Fatalf("ApplySync: %v", err)
		}
		return out.Results[0]
	}

	if got := send().Outcome; got != store.OutcomeApplied {
		t.Fatalf("first create: %q", got)
	}
	if got := send().Outcome; got != store.OutcomeAbsorbed {
		t.Errorf("second create: %q, want absorbed", got)
	}
}

// The rotation decides who has the first turn, not who happened to create it.
func TestTheFirstOccurrenceTakesTheRotationsTurn(t *testing.T) {
	w := newWorld(t)

	sara := w.addMember("Sara")
	series, _ := w.createSeries(weekly(map[string]any{
		"rotation":        []any{w.member.String(), sara.String()},
		"rotation_offset": float64(1),
	}))

	if who := w.assignee(store.OccurrenceID(series, 0)); who == nil || *who != sara {
		t.Errorf("assigned to %v, want Sara at offset 1", who)
	}
}

// Refused HERE, unlike on the advance path. The person is in front of the
// screen that produced the rule and can fix it in a second, whereas refusing a
// tick would destroy work somebody had already done.
func TestASeriesWithAnImpossibleScheduleIsRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"a rule outside the subset": weekly(map[string]any{"rule": "FREQ=YEARLY"}),
		"nonsense in the rule":      weekly(map[string]any{"rule": "FREQ=WEEKLY;BYDAY=XX"}),
		"no mode":                   weekly(map[string]any{"mode": ""}),
		"an unknown mode":           weekly(map[string]any{"mode": "whenever"}),
		"no title":                  weekly(map[string]any{"title": "  "}),
		"no anchor":                 weekly(map[string]any{"anchor_on": ""}),
		"an anchor that is not a date": weekly(map[string]any{
			"anchor_on": "la semaine prochaine",
		}),
		"relative with no interval": weekly(map[string]any{
			"mode": "relative", "rule": "",
		}),
		"a catch_up nobody defined": weekly(map[string]any{"catch_up": "panic"}),
		"a rotation of nonsense": weekly(map[string]any{
			"rotation": []any{"not-a-uuid"},
		}),
	}

	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			series, result := w.createSeries(fields)

			if result.Outcome != store.OutcomeRefused {
				t.Errorf("outcome %q, want refused", result.Outcome)
			}
			if result.Code != store.ReasonMalformed {
				t.Errorf("code %q, want malformed", result.Code)
			}

			// Nothing at all: not a series without an occurrence, and not an
			// occurrence without a series. A refusal leaves no trace to tidy.
			var rows int
			scanRow(t, &rows, `SELECT count(*) FROM series
			                    WHERE household_id = $1 AND id = $2`, w.id, series)
			if rows != 0 {
				t.Error("a refused series was written anyway")
			}

			scanRow(t, &rows, `SELECT count(*) FROM tasks
			                    WHERE household_id = $1 AND series_id = $2`,
				w.id, series)
			if rows != 0 {
				t.Error("a refused series left an occurrence behind")
			}
		})
	}
}

// A relative series is legitimate and has no rule at all: "three weeks after
// the last time", which only makes sense once somebody has done it once.
func TestARelativeSeriesNeedsNoRule(t *testing.T) {
	w := newWorld(t)

	series, result := w.createSeries(map[string]any{
		"title":     "Changer les draps",
		"mode":      "relative",
		"rel_days":  float64(21),
		"anchor_on": "2026-10-27",
		"catch_up":  "skip",
	})

	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("outcome %q (%s), want applied", result.Outcome, result.Code)
	}
	if open := w.openOccurrence(series); open == nil {
		t.Error("no occurrence opened")
	}
}

// An app too old to know about series says so by not sending one. A server
// that accepted it anyway would make the version number mean nothing.
func TestASeriesFromAnOlderProtocolIsRetryablyRefused(t *testing.T) {
	w := newWorld(t)

	out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "1.0.0",
		Protocol: 1,
		Ops: []store.Op{{
			OpID: uuid.New(), Kind: store.OpCreate,
			Entity: store.EntitySeries, RowID: uuid.New(), Fields: weekly(nil),
		}},
	})
	if err != nil {
		t.Fatalf("ApplySync: %v", err)
	}

	result := out.Results[0]
	if result.Code != store.ReasonUnsupportedOp {
		t.Errorf("code %q, want unsupported_op — which is RETRYABLE, so a "+
			"deploy fixes it rather than poisoning the queue", result.Code)
	}
}
