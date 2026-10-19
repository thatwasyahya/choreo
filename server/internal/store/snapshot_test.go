package store_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

func (w *world) snapshot() (store.SnapshotHeader, []store.SnapshotRow) {
	w.t.Helper()

	var (
		header store.SnapshotHeader
		rows   []store.SnapshotRow
	)

	count, err := w.store.Snapshot(w.t.Context(), w.id,
		func(h store.SnapshotHeader) error { header = h; return nil },
		func(r store.SnapshotRow) error { rows = append(rows, r); return nil },
	)
	if err != nil {
		w.t.Fatalf("Snapshot: %v", err)
	}
	if count != len(rows) {
		w.t.Fatalf("Snapshot reported %d rows but emitted %d", count, len(rows))
	}
	return header, rows
}

func has(rows []store.SnapshotRow, rowID uuid.UUID) bool {
	for _, r := range rows {
		if r.RowID == rowID {
			return true
		}
	}
	return false
}

// The cursor must come from the SAME transaction as the rows. Otherwise a write
// that commits mid-stream is absent from the body while carrying a rev below
// the cursor handed back — never redelivered, and the device is wrong about its
// household for good.
func TestSnapshotCursorMatchesWhatItContains(t *testing.T) {
	w := newWorld(t)
	id, _ := w.task("Poubelles")

	header, rows := w.snapshot()

	if !has(rows, id) {
		t.Fatal("the task is missing from the snapshot")
	}

	var head int64
	scanRow(t, &head, `SELECT change_seq FROM households WHERE id = $1`, w.id)
	if header.CursorSeq != head {
		t.Errorf("cursor = %d, want the head %d", header.CursorSeq, head)
	}

	for _, r := range rows {
		if r.Rev > header.CursorSeq {
			t.Errorf("row %v carries rev %d, above the cursor %d — it would never be "+
				"redelivered", r.RowID, r.Rev, header.CursorSeq)
		}
	}
}

// "No tombstones: there is no need to learn about the deletion of a row one has
// never heard of." It is also what makes a real purge possible.
func TestSnapshotCarriesNoTombstones(t *testing.T) {
	w := newWorld(t)
	alive, _ := w.task("Vivante")
	buried, _ := w.task("Enterrée")

	w.apply(newOp(store.OpDelete, store.EntityTask, buried, nil, nil))

	_, rows := w.snapshot()

	if !has(rows, alive) {
		t.Error("the live row is missing")
	}
	if has(rows, buried) {
		t.Error("a tombstone travelled in the snapshot")
	}
}

func TestSnapshotIncludesTheHouseholdTimezone(t *testing.T) {
	w := newWorld(t)

	header, _ := w.snapshot()
	if header.TZ != "Europe/Paris" {
		t.Errorf("tz = %q, want the household's own", header.TZ)
	}
}

// --- the dead cursor --------------------------------------------------------

// A cursor below the purge horizon has missed changes that no longer exist, so
// the device must rebuild. Said in a normal answer rather than an error,
// because the operations pushed in the same call are still applied: "your
// cursor expired" must never mean "your offline edits are lost".
func TestADeadCursorAsksForASnapshotWithoutLosingThePush(t *testing.T) {
	w := newWorld(t)
	w.task("Avant la purge")

	var head int64
	scanRow(t, &head, `SELECT change_seq FROM households WHERE id = $1`, w.id)

	execSQL(t, `UPDATE households SET purge_horizon_seq = $2 WHERE id = $1`, w.id, head)

	pushed := uuid.New()
	out, err := w.store.ApplySync(t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "1.0.0", Protocol: 1,
		CursorSeq: 1, // far below the horizon
		Ops: []store.Op{newOp(store.OpCreate, store.EntityTask, pushed,
			map[string]any{"title": "Écrite hors-ligne"}, nil)},
	})
	if err != nil {
		t.Fatalf("ApplySync: %v", err)
	}

	if !out.SnapshotRequired {
		t.Error("snapshot_required = false for a cursor below the horizon")
	}
	if got := out.Results[0].Outcome; got != store.OutcomeApplied {
		t.Errorf("the pushed operation was dropped: %q — an expired cursor must not "+
			"cost somebody their offline work", got)
	}
}

// A brand new device has cursor zero. That is its normal starting state, not a
// fault, so it must not be answered as one.
func TestANewDeviceIsNotTreatedAsBroken(t *testing.T) {
	w := newWorld(t)
	w.task("Quelque chose")

	execSQL(t, `UPDATE households SET purge_horizon_seq = 5 WHERE id = $1`, w.id)

	out, err := w.store.ApplySync(t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "1.0.0", Protocol: 1,
		CursorSeq: 0,
	})
	if err != nil {
		t.Fatalf("ApplySync: %v", err)
	}
	if out.SnapshotRequired {
		t.Error("a new device was told its cursor had expired")
	}
}

// --- notices ----------------------------------------------------------------

// staleWrite produces a notice by losing a field race, and returns its id.
func (w *world) staleNotice() uuid.UUID {
	w.t.Helper()

	id, first := w.task("Sortir les poubelles")
	w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Poubelles jaunes"}, map[string]int64{"title": first}))

	out := w.apply(newOp(store.OpSetFields, store.EntityTask, id,
		map[string]any{"title": "Descendre les poubelles"},
		map[string]int64{"title": first}))

	notice := out.Results[0].NoticeID
	if notice == nil {
		w.t.Fatal("no notice was produced")
	}
	return *notice
}

func resolveOp(noticeID uuid.UUID, choice string) store.Op {
	return newOp(store.OpResolveNotice, store.EntityNotice, noticeID,
		map[string]any{"choice": choice}, nil)
}

func TestResolvingANoticeClosesTheCard(t *testing.T) {
	w := newWorld(t)
	notice := w.staleNotice()

	result := w.apply(resolveOp(notice, store.ChoiceMine)).Results[0]
	if result.Outcome != store.OutcomeApplied {
		t.Fatalf("got %+v, want applied", result)
	}

	var resolved bool
	scanRow(t, &resolved, `SELECT resolved_rev IS NOT NULL FROM notices
	                        WHERE household_id = $1 AND id = $2`, w.id, notice)
	if !resolved {
		t.Error("the notice is still open")
	}
}

// Answering twice must be harmless: the reply travels in an outbox like any
// other operation, so it will be retried.
func TestResolvingANoticeTwiceIsAbsorbed(t *testing.T) {
	w := newWorld(t)
	notice := w.staleNotice()

	w.apply(resolveOp(notice, store.ChoiceTheirs))
	second := w.apply(resolveOp(notice, store.ChoiceTheirs)).Results[0]

	if second.Outcome != store.OutcomeAbsorbed {
		t.Errorf("outcome = %q, want absorbed", second.Outcome)
	}
}

// A decision is addressed to one person. Two voices answering it is a race, so
// somebody else's card is simply not there to be answered.
func TestCannotResolveSomebodyElsesNotice(t *testing.T) {
	w := newWorld(t)
	notice := w.staleNotice()
	other := w.addMember("Jordan")

	result := w.applyAs(other, resolveOp(notice, store.ChoiceMine)).Results[0]
	if result.Outcome != store.OutcomeAbsorbed {
		t.Errorf("outcome = %q, want absorbed — and nothing that reveals the card exists",
			result.Outcome)
	}

	var resolved bool
	scanRow(t, &resolved, `SELECT resolved_rev IS NOT NULL FROM notices
	                        WHERE household_id = $1 AND id = $2`, w.id, notice)
	if resolved {
		t.Error("somebody else closed the card")
	}
}

func TestResolvingWithoutAChoiceIsMalformed(t *testing.T) {
	w := newWorld(t)
	notice := w.staleNotice()

	result := w.apply(resolveOp(notice, "whatever")).Results[0]
	if result.Outcome != store.OutcomeRefused || result.Code != store.ReasonMalformed {
		t.Errorf("got %q/%q, want refused/malformed", result.Outcome, result.Code)
	}
}
