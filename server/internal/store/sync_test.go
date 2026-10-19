package store_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// Every test here is named after one line of the resolution table in
// docs/SYNC_PROTOCOL.md. The table is the specification, not an illustration:
// a handler written "by feel" drifts from it within a week, and the drift is
// invisible until somebody loses a chore.

type world struct {
	t      *testing.T
	store  *store.Store
	id     uuid.UUID
	member uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()

	s := newTestStore(t)
	acc := account(t, s, "camille")
	id, member := household(t, s, acc, "Maison")

	return &world{t: t, store: s, id: id, member: member.ID}
}

func newOp(kind, entity string, rowID uuid.UUID, fields map[string]any, base map[string]int64) store.Op {
	raw, err := json.Marshal(map[string]any{
		"kind": kind, "entity": entity, "row_id": rowID,
		"fields": fields, "base_revs": base,
	})
	if err != nil {
		panic(err)
	}
	return store.Op{
		OpID: uuid.New(), Kind: kind, Entity: entity, RowID: rowID,
		Fields: fields, BaseRevs: base, Raw: raw,
	}
}

func (w *world) apply(ops ...store.Op) store.SyncOutput {
	w.t.Helper()

	out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
		DeviceID:   uuid.New(),
		MemberID:   w.member,
		AppVersion: "1.0.0",
		Protocol:   1,
		Ops:        ops,
	})
	if err != nil {
		w.t.Fatalf("ApplySync: %v", err)
	}
	return out
}

// task creates one and returns its id and the rev its fields carry.
func (w *world) task(title string) (uuid.UUID, int64) {
	w.t.Helper()

	id := uuid.New()
	out := w.apply(newOp(store.OpCreate, store.EntityTask, id,
		map[string]any{"title": title}, nil))

	if out.Results[0].Outcome != store.OutcomeApplied {
		w.t.Fatalf("creating a task: %+v", out.Results[0])
	}
	return id, out.Results[0].Rev
}

// change finds a row in the pulled page.
func (w *world) change(out store.SyncOutput, rowID uuid.UUID) store.Change {
	w.t.Helper()

	for _, c := range out.Changes {
		if c.RowID == rowID {
			return c
		}
	}
	w.t.Fatalf("row %v is not in the page of %d changes", rowID, len(out.Changes))
	return store.Change{}
}

// --- the table, line by line ------------------------------------------------

// "base_rev == field_rev → applied. Over 99% of traffic: there is no conflict."
func TestRuleFieldWriteWithCurrentBaseIsApplied(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Appeler le plombier")

	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Appeler le plombier (devis)"},
		map[string]int64{"title": rev}))

	if got := out.Results[0].Outcome; got != store.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied — %+v", got, out.Results[0])
	}
	state := w.change(out, id).State
	if state["title"] != "Appeler le plombier (devis)" {
		t.Errorf("title = %v, want the new one", state["title"])
	}
}

// "field_rev absent → applied. There is nothing to conflict with."
func TestRuleNeverWrittenFieldIsApplied(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Poubelles")

	// notes was never written, so no base_rev can exist for it.
	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"notes": "bac jaune le mardi"}, nil))

	if got := out.Results[0].Outcome; got != store.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied — %+v", got, out.Results[0])
	}
}

// "base_rev < field_rev, or absent while field_rev exists → the SERVER's value
// holds, receipted, notice to the author carrying their text intact."
func TestRuleStaleFieldWriteLosesButIsNotThrownAway(t *testing.T) {
	w := newWorld(t)
	id, first := w.task("Sortir les poubelles")

	// Somebody else renames it. Now field_rev has moved on.
	w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Sortir les poubelles jaunes"},
		map[string]int64{"title": first}))

	// A phone that had been offline writes against the revision it had seen.
	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Descendre les poubelles"},
		map[string]int64{"title": first}))

	result := out.Results[0]
	if result.Outcome != store.OutcomeReceipted {
		t.Fatalf("outcome = %q, want receipted — %+v", result.Outcome, result)
	}
	if result.NoticeID == nil {
		t.Fatal("no notice: the author's text would be lost, which is the one thing forbidden")
	}

	// The server's value held.
	if title := w.change(out, id).State["title"]; title != "Sortir les poubelles jaunes" {
		t.Errorf("title = %v, want the value that was already synchronised", title)
	}

	// And the notice carries what the author typed, verbatim.
	notice := noticeIn(t, out)
	mine, _ := notice.State["mine"].(map[string]any)
	if mine["title"] != "Descendre les poubelles" {
		t.Errorf("the notice does not carry the author's text: %v", mine)
	}
}

// "base_rev > field_rev → malformed. Impossible, so it is our bug."
func TestRuleImpossibleBaseRevIsMalformed(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Poubelles")

	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Autre chose"},
		map[string]int64{"title": rev + 1000}))

	result := out.Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonMalformed {
		t.Errorf("got %q/%q, want refused/malformed", result.Outcome, result.Code)
	}
}

// "set_fields on an unknown row → refused target_gone, content kept. A field
// write NEVER creates a row."
func TestRuleFieldWriteNeverCreatesARow(t *testing.T) {
	w := newWorld(t)

	ghost := uuid.New()
	out := w.apply(newOp(store.OpSetFields, store.EntityTask, ghost,
		map[string]any{"title": "Ressuscité"}, nil))

	result := out.Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonTargetGone {
		t.Errorf("got %q/%q, want refused/target_gone", result.Outcome, result.Code)
	}

	for _, c := range out.Changes {
		if c.RowID == ghost {
			t.Error("the row was created anyway — a purged row would resurrect piecemeal")
		}
	}
}

// "Edit against delete → the deletion holds the display, the edit is not thrown
// away, a decision notice addressed to the EDITOR."
func TestRuleEditAgainstDeleteNotifiesTheEditor(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Acheter du lait")

	// The delete operation lands in a later lot; the tombstone is what matters
	// to this rule, so it is set directly.
	execSQL(t, `UPDATE tasks SET deleted_rev = rev WHERE household_id = $1 AND id = $2`,
		w.id, id)

	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Acheter du lait entier"},
		map[string]int64{"title": rev}))

	result := out.Results[0]
	if result.Outcome != store.OutcomeReceipted || result.NoticeID == nil {
		t.Fatalf("got %+v, want receipted with a notice", result)
	}

	notice := noticeIn(t, out)
	if notice.State["kind"] != "decision" {
		t.Errorf("notice kind = %v, want decision", notice.State["kind"])
	}
	if notice.State["for_member"] != w.member.String() {
		t.Errorf("the notice is addressed to %v, want the editor %v — two voices are a race",
			notice.State["for_member"], w.member)
	}
}

// "Unknown operation kind → refused unsupported_op, RETRYABLE, never terminal."
func TestRuleUnknownOperationIsRetryable(t *testing.T) {
	w := newWorld(t)

	out := w.apply(newOp("teleport", store.EntityTask, uuid.New(),
		map[string]any{"title": "x"}, nil))

	result := out.Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonUnsupportedOp {
		t.Errorf("got %q/%q, want refused/unsupported_op", result.Outcome, result.Code)
	}
}

// "Schema violated → refused malformed, logged, NEVER shown."
func TestRuleWrongTypeIsMalformed(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Poubelles")

	// priority is a smallint; a string cannot go there.
	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"priority": "haute"},
		map[string]int64{"priority": rev}))

	result := out.Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonMalformed {
		t.Errorf("got %q/%q, want refused/malformed", result.Outcome, result.Code)
	}
}

// "op_id already seen, identical hash → absorbed, the stored result returned."
func TestRuleReplayIsAbsorbed(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Poubelles")

	op := newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Poubelles jaunes"}, map[string]int64{"title": rev})

	first := w.apply(op).Results[0]
	second := w.apply(op).Results[0]

	if second.Outcome != store.OutcomeAbsorbed {
		t.Fatalf("outcome = %q, want absorbed", second.Outcome)
	}
	if second.Rev != first.Rev {
		t.Errorf("rev = %d, want the stored %d — a retry must land on the same answer",
			second.Rev, first.Rev)
	}
}

// "op_id already seen, different hash → malformed. Without this a mutation
// disappears with no trace at all."
func TestRuleReusedOpIDWithDifferentPayloadIsMalformed(t *testing.T) {
	w := newWorld(t)
	id, rev := w.task("Poubelles")

	first := newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Poubelles jaunes"}, map[string]int64{"title": rev})
	w.apply(first)

	// The same op_id, a different intent. A client bug, and one that must not
	// be answered with the other operation's result.
	second := newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Tout autre chose"}, map[string]int64{"title": rev})
	second.OpID = first.OpID

	result := w.apply(second).Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonMalformed {
		t.Errorf("got %q/%q, want refused/malformed", result.Outcome, result.Code)
	}
}

// --- creation and forward compatibility -------------------------------------

// Two phones creating the same derived row are not in conflict: they agree.
func TestCreatingTheSameRowTwiceIsAbsorbed(t *testing.T) {
	w := newWorld(t)

	id := uuid.New()
	fields := map[string]any{"title": "Poubelles"}

	if got := w.apply(newOp(store.OpCreate, store.EntityTask, id, fields, nil)).Results[0]; got.Outcome != store.OutcomeApplied {
		t.Fatalf("first create: %+v", got)
	}
	if got := w.apply(newOp(store.OpCreate, store.EntityTask, id, fields, nil)).Results[0]; got.Outcome != store.OutcomeAbsorbed {
		t.Errorf("second create: outcome = %q, want absorbed", got.Outcome)
	}
}

// A field this server has no column for must survive rather than be refused:
// that is half of what makes N-2 compatibility free.
func TestUnknownFieldFromANewerAppIsKept(t *testing.T) {
	w := newWorld(t)

	id := uuid.New()
	out := w.apply(newOp(store.OpCreate, store.EntityTask, id,
		map[string]any{"title": "Poubelles", "colour": "teal"}, nil))

	if out.Results[0].Outcome != store.OutcomeApplied {
		t.Fatalf("create: %+v", out.Results[0])
	}

	ext, _ := w.change(out, id).State["ext"].(map[string]any)
	if ext["colour"] != "teal" {
		t.Errorf("ext = %v, want the unknown field kept — an older server must not "+
			"discard a newer app's write", ext)
	}
}

// Only the title is ever mandatory (product principle 1).
func TestCreateNeedsOnlyATitle(t *testing.T) {
	w := newWorld(t)

	empty := w.apply(newOp(store.OpCreate, store.EntityTask, uuid.New(),
		map[string]any{"notes": "sans titre"}, nil)).Results[0]
	if empty.Outcome != store.OutcomeRefused {
		t.Errorf("a task with no title was accepted: %+v", empty)
	}
}

// --- the batch --------------------------------------------------------------

// "Each operation is its own transaction, applied in the order of the array."
// A refused one must not roll back the good ones in front of it — which is what
// matters on the day someone comes back after ten days.
func TestARefusedOperationDoesNotRollBackTheGoodOnes(t *testing.T) {
	w := newWorld(t)

	good := uuid.New()
	out := w.apply(
		newOp(store.OpCreate, store.EntityTask, good, map[string]any{"title": "Bonne"}, nil),
		newOp(store.OpSetFields, store.EntityTask, uuid.New(), map[string]any{"title": "Fantôme"}, nil),
		newOp(store.OpCreate, store.EntityTask, uuid.New(), map[string]any{"title": "Aussi bonne"}, nil),
	)

	if len(out.Results) != 3 {
		t.Fatalf("got %d results, want one per operation", len(out.Results))
	}
	if out.Results[0].Outcome != store.OutcomeApplied {
		t.Errorf("the first operation was rolled back: %+v", out.Results[0])
	}
	if out.Results[1].Outcome != store.OutcomeRefused {
		t.Errorf("the ghost write was not refused: %+v", out.Results[1])
	}
	if out.Results[2].Outcome != store.OutcomeApplied {
		t.Errorf("the third operation was lost: %+v", out.Results[2])
	}
	w.change(out, good) // fails the test if it is missing from the page
}

// A cursor must not miss anything: everything written lands above it.
func TestCursorSeesEverythingWrittenAfterIt(t *testing.T) {
	w := newWorld(t)

	first := w.apply()
	mark := first.CursorSeq

	id, _ := w.task("Après le curseur")

	out, err := w.store.ApplySync(t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "1.0.0", Protocol: 1,
		CursorSeq: mark,
	})
	if err != nil {
		t.Fatalf("ApplySync: %v", err)
	}

	w2 := &world{t: t, store: w.store, id: w.id, member: w.member}
	w2.change(out, id)

	if out.CursorSeq <= mark {
		t.Errorf("cursor = %d, did not advance past %d", out.CursorSeq, mark)
	}
}

func noticeIn(t *testing.T, out store.SyncOutput) store.Change {
	t.Helper()

	for _, c := range out.Changes {
		if c.Entity == "notice" {
			return c
		}
	}
	t.Fatal("no notice in the page")
	return store.Change{}
}
