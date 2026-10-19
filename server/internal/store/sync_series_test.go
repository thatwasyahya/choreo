package store_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// applyAs sends operations as somebody else in the household.
func (w *world) applyAs(member uuid.UUID, ops ...store.Op) store.SyncOutput {
	w.t.Helper()

	out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: member, AppVersion: "1.0.0", Protocol: 1,
		Ops: ops,
	})
	if err != nil {
		w.t.Fatalf("ApplySync: %v", err)
	}
	return out
}

// member adds an account-less member, as a household would for a child.
func (w *world) addMember(name string) uuid.UUID {
	w.t.Helper()

	id := uuid.New()
	execSQL(w.t, `INSERT INTO members
	    (id, household_id, account_id, role, rev, created_rev, updated_by, fields)
	    VALUES ($1, $2, NULL, 'adult', 2, 2, $1,
	            jsonb_build_object('display_name', $3::text))`,
		id, w.id, name)
	return id
}

// series creates a series and its first occurrence, wired to the register.
func (w *world) series(title string) (seriesID, first uuid.UUID) {
	w.t.Helper()

	seriesID = uuid.New()
	first = store.OccurrenceID(seriesID, 0)

	execSQL(w.t, `INSERT INTO series
	    (id, household_id, rev, created_rev, updated_by, title, mode, anchor_on, next_index)
	    VALUES ($1, $2, 2, 2, $3, $4, 'calendar', current_date, 0)`,
		seriesID, w.id, w.member, title)

	execSQL(w.t, `INSERT INTO tasks
	    (id, household_id, rev, created_rev, updated_by, kind, title, series_id, series_index)
	    VALUES ($1, $2, 3, 3, $3, 'task', $4, $5, 0)`,
		first, w.id, w.member, title, seriesID)

	execSQL(w.t, `UPDATE series SET open_occurrence_id = $3
	               WHERE household_id = $1 AND id = $2`, w.id, seriesID, first)

	return seriesID, first
}

func (w *world) openOccurrence(seriesID uuid.UUID) *uuid.UUID {
	w.t.Helper()

	var open *uuid.UUID
	scanRow(w.t, &open, `SELECT open_occurrence_id FROM series
	                      WHERE household_id = $1 AND id = $2`, w.id, seriesID)
	return open
}

func (w *world) completions(out store.SyncOutput, rowID uuid.UUID) []any {
	w.t.Helper()

	list, _ := w.change(out, rowID).State["completions"].([]any)
	return list
}

// --- completions ------------------------------------------------------------

// "Two completions of the same item → BOTH are kept. A set indexed by member.
// It is not a conflict, so we do not resolve it: we do not create it."
func TestRuleTwoCompletionsAreBothKept(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Sortir les poubelles")
	other := w.addMember("Jordan")

	w.apply(completeOp(id))
	out := w.applyAs(other, completeOp(id))

	if got := len(w.completions(out, id)); got != 2 {
		t.Errorf("got %d completions, want 2 — two adults ticking are not in conflict", got)
	}
}

// The same member ticking twice must not be counted twice: completion_id is the
// deduplication key for points.
func TestTickingTwiceAsTheSameMemberIsAbsorbed(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Poubelles")

	w.apply(completeOp(id))
	second := w.apply(completeOp(id)).Results[0]

	if second.Outcome != store.OutcomeAbsorbed {
		t.Errorf("outcome = %q, want absorbed", second.Outcome)
	}
}

// "Uncomplete removes EXACTLY the members the author had observed. Unticking in
// the kitchen does not erase a completion recorded in the garage ten seconds
// earlier."
func TestRuleUncompleteRemovesOnlyWhatWasObserved(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Poubelles")
	garage := w.addMember("Jordan")

	w.apply(completeOp(id))           // the author, in the kitchen
	w.applyAs(garage, completeOp(id)) // somebody else, in the garage

	// The author unticks, having seen only their own completion.
	op := newOp(store.OpUncomplete, store.EntityTask, id, nil, nil)
	op.Observed = []uuid.UUID{w.member}
	op = seal(op)
	out := w.apply(op)

	left := w.completions(out, id)
	if len(left) != 1 {
		t.Fatalf("got %d completions left, want 1", len(left))
	}
	first, _ := left[0].(map[string]any)
	if first["member_id"] != garage.String() {
		t.Errorf("the surviving completion is %v, want the one from the garage", first["member_id"])
	}
}

// occurred_on is proposed by the client and bounded by the server, against the
// SERVER's date. A wrong clock is remembered as suspect, never believed.
func TestOccurredOnIsBoundedAndFlagged(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Relevé de compteur")

	op := completeOp(id)
	op.OccurredOn = "2020-01-01" // far outside the window
	out := w.apply(op)

	entry, _ := w.completions(out, id)[0].(map[string]any)
	if entry["clock_suspect"] != true {
		t.Errorf("clock_suspect = %v, want true", entry["clock_suspect"])
	}
	if entry["occurred_on"] == "2020-01-01" {
		t.Error("the proposed date was believed instead of bounded")
	}
}

// --- deletion ---------------------------------------------------------------

// "Stale deletion (the editor had won) → receipted, notice to the author of the
// deletion. A deletion destroys: it asks, it does not impose."
func TestRuleStaleDeleteAsksRatherThanImposes(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Acheter du lait")

	// Somebody edits it after this author last looked.
	w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Acheter du lait entier"},
		map[string]int64{"title": rev}))

	stale := newOp(store.OpDelete, store.EntityTask, id, nil, nil)
	stale.BaseRev = &rev
	out := w.apply(stale)

	result := out.Results[0]
	if result.Outcome != store.OutcomeReceipted || result.Code != store.ReasonStaleDelete {
		t.Fatalf("got %q/%q, want receipted/stale_delete", result.Outcome, result.Code)
	}
	if result.NoticeID == nil {
		t.Error("no notice: the person who asked for the deletion is never told")
	}
	if deleted := w.change(out, id).Deleted; deleted {
		t.Error("the row was buried anyway — the editor's work was discarded")
	}
}

// --- the series invariant ---------------------------------------------------

// "Two phones open the next occurrence → derived identity: it is the SAME row."
func TestRuleDerivedOccurrenceIdentityIsStable(t *testing.T) {
	seriesID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	if store.OccurrenceID(seriesID, 7) == store.OccurrenceID(seriesID, 8) {
		t.Error("two indices derive the same id")
	}

	// It must depend on an integer only. A date-derived identity would mint two
	// different ids for one occurrence during a rolling deploy, on nothing more
	// than a tzdata skew.
	const expected = "9afc3a86-9519-5a41-85ce-798de5756e30"
	if got := store.OccurrenceID(seriesID, 7).String(); got != expected {
		t.Errorf("OccurrenceID = %s, want %s — changing the derivation breaks the "+
			"invariant for every existing household", got, expected)
	}
}

func TestCompletingAnOccurrenceAdvancesTheSeries(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	result := w.apply(completeOp(first)).Results[0]
	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("completing: %+v", result)
	}
	if result.Fields["series_advanced"] != "true" {
		t.Errorf("series_advanced = %q, want true", result.Fields["series_advanced"])
	}

	open := w.openOccurrence(seriesID)
	if open == nil {
		t.Fatal("the series has no open occurrence — the invariant is exactly one, not at most one")
	}
	if *open == first {
		t.Error("the register still points at the occurrence that was just ticked")
	}
	if want := store.OccurrenceID(seriesID, 1); *open != want {
		t.Errorf("open = %v, want the derived %v", *open, want)
	}
}

// "Deleting the open occurrence advances the series." That is what makes the
// invariant exactly-one: "we are skipping this week" cannot leave a series with
// nothing open — and it is why no sweeper regenerates what somebody dismissed.
func TestDeletingTheOpenOccurrenceAdvancesTheSeries(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	result := w.apply(newOp(store.OpDelete, store.EntityTask, first, nil, nil)).Results[0]
	if result.Fields["series_advanced"] != "true" {
		t.Fatalf("series_advanced = %q, want true — %+v", result.Fields["series_advanced"], result)
	}

	if open := w.openOccurrence(seriesID); open == nil || *open == first {
		t.Error("skipping a week left the series with nothing open")
	}
}

// The sentence the protocol repeats twice: zero rows updated is NOT an error,
// and a human's tick is never destroyed by a constraint failure.
func TestRuleACompletionSurvivesAFailedAdvance(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	// A paused series cannot advance.
	execSQL(t, `UPDATE series SET paused = true WHERE household_id = $1 AND id = $2`,
		w.id, seriesID)

	out := w.apply(completeOp(first))
	result := out.Results[0]

	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("the tick failed: %+v — a finished chore must not disappear", result)
	}
	if result.Fields["series_advanced"] != "false" {
		t.Errorf("series_advanced = %q, want false", result.Fields["series_advanced"])
	}
	if got := len(w.completions(out, first)); got != 1 {
		t.Errorf("got %d completions, want the tick kept", got)
	}
}

// "Uncompleting a passed occurrence → successor rule: cancel an UNTOUCHED
// successor, otherwise refuse."
func TestRuleUncompletingCancelsAnUntouchedSuccessor(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	w.apply(completeOp(first)) // advances; the successor is created, untouched

	op := newOp(store.OpUncomplete, store.EntityTask, first, nil, nil)
	op.Observed = []uuid.UUID{w.member}
	op = seal(op)
	result := w.apply(op).Results[0]

	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("got %+v, want applied — an untouched successor is cancellable", result)
	}

	open := w.openOccurrence(seriesID)
	if open == nil || *open != first {
		t.Errorf("open = %v, want the register back on %v", open, first)
	}
}

func TestRuleUncompletingIsRefusedWhenTheSuccessorIsInUse(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	w.apply(completeOp(first))
	successor := store.OccurrenceID(seriesID, 1)

	// Somebody has already worked on the next one.
	execSQL(t, `UPDATE tasks SET rev = rev + 1, title = 'Poubelles (bac jaune)'
	             WHERE household_id = $1 AND id = $2`, w.id, successor)

	op := newOp(store.OpUncomplete, store.EntityTask, first, nil, nil)
	op.Observed = []uuid.UUID{w.member}
	op = seal(op)
	result := w.apply(op).Results[0]

	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonSuccessorInUse {
		t.Fatalf("got %q/%q, want refused/successor_in_use", result.Outcome, result.Code)
	}

	// And the refusal left nothing behind: the completion is still there.
	var left int
	scanRow(t, &left, `SELECT count(*) FROM completions
	                    WHERE household_id = $1 AND row_id = $2`, w.id, first)
	if left != 1 {
		t.Errorf("%d completions left, want 1 — a refusal must not half-apply", left)
	}
}

func TestRestoringGoesThroughTheSuccessorRule(t *testing.T) {
	w := newWorld(t)
	seriesID, first := w.series("Poubelles")

	w.apply(newOp(store.OpDelete, store.EntityTask, first, nil, nil)) // advances
	successor := store.OccurrenceID(seriesID, 1)

	execSQL(t, `UPDATE tasks SET rev = rev + 1 WHERE household_id = $1 AND id = $2`,
		w.id, successor)

	result := w.apply(newOp(store.OpRestore, store.EntityTask, first, nil, nil)).Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonSuccessorInUse {
		t.Errorf("got %q/%q, want refused/successor_in_use", result.Outcome, result.Code)
	}
}

func completeOp(rowID uuid.UUID) store.Op {
	op := newOp(store.OpComplete, store.EntityTask, rowID, nil, nil)
	op.CompletionID = uuid.New()
	return seal(op)
}
