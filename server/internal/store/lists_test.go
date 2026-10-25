package store_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// A shared checklist writes a second and a third table through the SAME
// arbitration as a chore. These tests exist to prove that generalising it did
// not quietly loosen it: every refusal a task gets, an item gets too, and a
// refused operation leaves nothing behind on either table.

// applyAt sends operations at a stated protocol version, which is the whole
// point for an entity that joined the vocabulary at version three.
func (w *world) applyAt(protocol int, ops ...store.Op) store.SyncOutput {
	w.t.Helper()

	out, err := w.store.ApplySync(w.t.Context(), w.id, store.SyncInput{
		DeviceID: uuid.New(), MemberID: w.member, AppVersion: "3.0.0",
		Protocol: protocol, Ops: ops,
	})
	if err != nil {
		w.t.Fatalf("ApplySync: %v", err)
	}
	return out
}

// list opens one and returns its id.
func (w *world) list(name string) uuid.UUID {
	w.t.Helper()

	id := uuid.New()
	out := w.applyAt(3, newOp(store.OpCreate, store.EntityList, id,
		map[string]any{"name": name}, nil))
	if out.Results[0].Outcome != store.OutcomeApplied {
		w.t.Fatalf("opening a list: %+v", out.Results[0])
	}
	return id
}

// item puts one line on a list, at a position.
func (w *world) item(list uuid.UUID, label, orderKey string) (uuid.UUID, int64) {
	w.t.Helper()

	id := uuid.New()
	out := w.applyAt(3, newOp(store.OpCreate, store.EntityListItem, id, map[string]any{
		"list_id": list.String(), "label": label, "order_key": orderKey,
	}, nil))
	if out.Results[0].Outcome != store.OutcomeApplied {
		w.t.Fatalf("adding %q: %+v", label, out.Results[0])
	}
	return id, out.Results[0].Rev
}

func (w *world) rowsIn(table string, id uuid.UUID) int {
	w.t.Helper()

	var rows int
	//nolint:gosec // a literal from this file, not from anything on the wire
	scanRow(w.t, &rows, "SELECT count(*) FROM "+table+
		" WHERE household_id = $1 AND id = $2", w.id, id)
	return rows
}

// A list is a name and nothing else. Which means the name is the one thing it
// cannot be without — and a blank one is not a name.
func TestAListRefusesToExistWithoutAName(t *testing.T) {
	cases := map[string]map[string]any{
		"no name at all":          {},
		"an empty name":           {"name": ""},
		"only spaces":             {"name": "   "},
		"a name that is a number": {"name": 7.0},
		"a name past the column bound": {
			"name": strings.Repeat("é", 201),
		},
		"an unknown kind": {"name": "Courses", "kind": "aisle-sorted"},
	}

	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)

			id := uuid.New()
			out := w.applyAt(3,
				newOp(store.OpCreate, store.EntityList, id, fields, nil))

			if got := out.Results[0]; got.Outcome != store.OutcomeRefused ||
				got.Code != store.ReasonMalformed {
				t.Errorf("outcome %q (%s), want refused malformed", got.Outcome, got.Code)
			}
			if w.rowsIn("lists", id) != 0 {
				t.Error("a refused list was written anyway")
			}
		})
	}
}

// The 201st accented character is the interesting one: PostgreSQL counts
// characters, so a bound counted in BYTES would have refused a perfectly legal
// name of 150 accented letters — and worse, would have let a legal-looking one
// through to a CHECK that aborts the transaction. An aborted transaction is a
// 500, which is our problem; an over-long name is theirs, and answerable.
func TestAnOverLongNameIsRefusedAndTheBatchCarriesOn(t *testing.T) {
	w := newWorld(t)

	tooLong := uuid.New()
	fine := uuid.New()
	out := w.applyAt(3,
		newOp(store.OpCreate, store.EntityList, tooLong,
			map[string]any{"name": strings.Repeat("a", 201)}, nil),
		newOp(store.OpCreate, store.EntityList, fine,
			map[string]any{"name": strings.Repeat("a", 200)}, nil))

	if got := out.Results[0]; got.Code != store.ReasonMalformed {
		t.Errorf("the long name came back %q (%s), want malformed", got.Outcome, got.Code)
	}
	// The one that fits is applied, in the same batch: the refusal was decided
	// in Go and never reached a constraint, so nothing was rolled back.
	if got := out.Results[1]; got.Outcome != store.OutcomeApplied {
		t.Errorf("the name that fits came back %q (%s), want applied", got.Outcome, got.Code)
	}
	if w.rowsIn("lists", fine) != 1 {
		t.Error("the name that fits was not written")
	}
}

// An item hangs off a list, and the list has to be there to hang it on.
func TestAnItemRefusesToExistWithoutALiveList(t *testing.T) {
	w := newWorld(t)
	live := w.list("Courses")

	cases := map[struct{ name, code string }]map[string]any{
		{"no label", store.ReasonMalformed}:                 {"list_id": live.String()},
		{"a blank label", store.ReasonMalformed}:            {"list_id": live.String(), "label": "  "},
		{"no list at all", store.ReasonMalformed}:           {"label": "Lait"},
		{"a list that is not an id", store.ReasonMalformed}: {"list_id": "la liste", "label": "Lait"},
		{"a quantity that is a word", store.ReasonMalformed}: {
			"list_id": live.String(), "label": "Lait", "qty": "deux",
		},
		{"a list nobody has ever seen", store.ReasonTargetGone}: {
			"list_id": uuid.NewString(), "label": "Lait",
		},
	}

	for want, fields := range cases {
		t.Run(want.name, func(t *testing.T) {
			id := uuid.New()
			out := w.applyAt(3,
				newOp(store.OpCreate, store.EntityListItem, id, fields, nil))

			if got := out.Results[0]; got.Outcome != store.OutcomeRefused ||
				got.Code != want.code {
				t.Errorf("outcome %q (%s), want refused %s",
					got.Outcome, got.Code, want.code)
			}
			if w.rowsIn("list_items", id) != 0 {
				t.Error("a refused item was written anyway")
			}
		})
	}
}

// Somebody adds a line while underground; the list was deleted an hour ago.
//
// Their words must come back to them. A line typed on the way to the shop is
// work, and the rule is the same one that governs a field write against a
// purged row: refuse the write, keep the text, tell the author.
func TestALineAddedToADeletedListComesBackWithItsWords(t *testing.T) {
	w := newWorld(t)
	list := w.list("Courses")

	out := w.applyAt(3, newOp(store.OpDelete, store.EntityList, list, nil, nil))
	if got := out.Results[0]; got.Outcome != store.OutcomeApplied {
		t.Fatalf("deleting the list: %+v", got)
	}

	item := uuid.New()
	out = w.applyAt(3, newOp(store.OpCreate, store.EntityListItem, item, map[string]any{
		"list_id": list.String(), "label": "Beurre demi-sel",
	}, nil))

	got := out.Results[0]
	if got.Outcome != store.OutcomeRefused || got.Code != store.ReasonTargetGone {
		t.Fatalf("outcome %q (%s), want refused target_gone", got.Outcome, got.Code)
	}
	if got.NoticeID == nil {
		t.Fatal("the author was told nothing")
	}

	var kept string
	scanRow(t, &kept, `SELECT mine->>'label' FROM notices
	                    WHERE household_id = $1 AND id = $2`, w.id, *got.NoticeID)
	if kept != "Beurre demi-sel" {
		t.Errorf("the notice kept %q, want the words that were typed", kept)
	}
}

// An item does not move to another list by a field write, any more than a task
// becomes an event by one. Both are the row's identity, set once.
func TestAnItemCannotChangeListsByAFieldWrite(t *testing.T) {
	w := newWorld(t)
	here := w.list("Courses")
	there := w.list("Valise")
	item, _ := w.item(here, "Lait", "a")

	out := w.applyAt(3, newOp(store.OpSetFields, store.EntityListItem, item,
		map[string]any{"list_id": there.String()}, nil))

	if got := out.Results[0]; got.Outcome != store.OutcomeRefused ||
		got.Code != store.ReasonMalformed {
		t.Errorf("outcome %q (%s), want refused malformed", got.Outcome, got.Code)
	}

	var belongs uuid.UUID
	scanRow(t, &belongs, `SELECT list_id FROM list_items
	                       WHERE household_id = $1 AND id = $2`, w.id, item)
	if belongs != here {
		t.Errorf("the item moved to %v anyway", belongs)
	}
}

// A phone from before this deploy gets a retryable no, not a bug report.
//
// Retryable is the whole point: the next deploy drains the outbox instead of
// the operation poisoning it forever.
func TestAClientTooOldToKnowAboutListsIsToldToComeBack(t *testing.T) {
	w := newWorld(t)

	id := uuid.New()
	out := w.applyAt(protocolBeforeLists,
		newOp(store.OpCreate, store.EntityList, id,
			map[string]any{"name": "Courses"}, nil))

	if got := out.Results[0]; got.Outcome != store.OutcomeRefused ||
		got.Code != store.ReasonUnsupportedOp {
		t.Errorf("outcome %q (%s), want refused unsupported_op", got.Outcome, got.Code)
	}
	if w.rowsIn("lists", id) != 0 {
		t.Error("a list was written for a client that cannot read it")
	}
}

// protocolBeforeLists is the version a phone that predates this lot speaks.
const protocolBeforeLists = 2

// Ticking a list is not a thing, and saying so must be TERMINAL.
//
// unsupported_op would be wrong here: it invites a retry, and no deploy will
// ever make a ticked list mean anything. The distinction is the difference
// between an outbox that drains and one that spins.
func TestTickingAWholeListIsMalformedAndNotRetryable(t *testing.T) {
	w := newWorld(t)
	list := w.list("Courses")

	out := w.applyAt(3, store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: store.EntityList,
		RowID: list, CompletionID: uuid.New(), Raw: []byte(`{}`),
	})

	if got := out.Results[0]; got.Outcome != store.OutcomeRefused ||
		got.Code != store.ReasonMalformed {
		t.Errorf("outcome %q (%s), want refused malformed", got.Outcome, got.Code)
	}
}

// An item is ticked by the same operation as a chore, and the tick lands in the
// same table — so "who took the milk" is answered without a second mechanism.
func TestTickingAnItemRecordsWhoTookIt(t *testing.T) {
	w := newWorld(t)
	list := w.list("Courses")
	item, _ := w.item(list, "Lait", "a")

	out := w.applyAt(3, store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: store.EntityListItem,
		RowID: item, CompletionID: uuid.New(), Raw: []byte(`{}`),
	})
	if got := out.Results[0]; got.Outcome != store.OutcomeApplied {
		t.Fatalf("ticking an item: %+v", got)
	}

	var who uuid.UUID
	scanRow(t, &who, `SELECT member_id FROM completions
	                   WHERE household_id = $1 AND row_id = $2`, w.id, item)
	if who != w.member {
		t.Errorf("the tick is attributed to %v, want the member who sent it", who)
	}

	// And it travels inside the item's own state, so a second phone reads it
	// off the row rather than asking a second question.
	page := w.applyAt(3)
	ticks, _ := w.change(page, item).State["completions"].([]any)
	if len(ticks) != 1 {
		t.Errorf("the item carries %d completions down the wire, want 1", len(ticks))
	}
}

// Untick clears the tick and leaves the row alone otherwise. An item has no
// closed_rev to disagree with its completions, and this is what proves the
// generalised write did not invent one.
func TestUntickingAnItemLeavesTheRowReadable(t *testing.T) {
	w := newWorld(t)
	list := w.list("Courses")
	item, _ := w.item(list, "Lait", "a")

	w.applyAt(3, store.Op{
		OpID: uuid.New(), Kind: store.OpComplete, Entity: store.EntityListItem,
		RowID: item, CompletionID: uuid.New(), Raw: []byte(`{}`),
	})

	out := w.applyAt(3, store.Op{
		OpID: uuid.New(), Kind: store.OpUncomplete, Entity: store.EntityListItem,
		RowID: item, Observed: []uuid.UUID{w.member}, Raw: []byte(`{}`),
	})
	if got := out.Results[0]; got.Outcome != store.OutcomeApplied {
		t.Fatalf("unticking an item: %+v", got)
	}

	var left int
	scanRow(t, &left, `SELECT count(*) FROM completions
	                    WHERE household_id = $1 AND row_id = $2`, w.id, item)
	if left != 0 {
		t.Errorf("%d completions survived the untick", left)
	}

	var label string
	scanRow(t, &label, `SELECT label FROM list_items
	                     WHERE household_id = $1 AND id = $2`, w.id, item)
	if label != "Lait" {
		t.Errorf("the item reads %q after an untick", label)
	}
}
