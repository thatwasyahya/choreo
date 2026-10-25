package httpapi_test

import (
	"slices"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// A shared list, over HTTP, between two phones and one real database.
//
// The unit tests say each rule is right. These say the path is — and the path
// is where the last two real defects were: a page that carried a row kind the
// snapshot knew about, and a client that quarantined what it was sent.

// startsList queues what a phone sends when somebody opens a new list.
func (p *phone) startsList(name string) string {
	p.t.Helper()

	id := uuid.NewString()
	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "create", "entity": "list",
		"row_id": id, "fields": map[string]any{"name": name},
	})
	return id
}

// adds puts one line on a list, at a position between its neighbours.
func (p *phone) adds(list, label, orderKey string) string {
	p.t.Helper()

	id := uuid.NewString()
	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "create", "entity": "list_item",
		"row_id": id,
		"fields": map[string]any{
			"list_id": list, "label": label, "order_key": orderKey,
		},
	})
	return id
}

// moves rewrites one position, carrying the revision this phone had in front
// of it — which is what makes two people tidying at once a non-event.
func (p *phone) moves(item, orderKey string) {
	p.t.Helper()

	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "set_fields", "entity": "list_item",
		"row_id":    item,
		"fields":    map[string]any{"order_key": orderKey},
		"base_revs": map[string]any{"order_key": p.fieldRev(item, "order_key")},
	})
}

// checks ticks an item: the same operation as ticking a chore.
func (p *phone) checks(item string) string {
	p.t.Helper()

	completion := uuid.NewString()
	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "complete", "entity": "list_item",
		"row_id": item, "completion_id": completion,
	})
	return completion
}

// drops buries a row of any entity.
func (p *phone) drops(entity, rowID string) {
	p.t.Helper()

	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "delete", "entity": entity,
		"row_id": rowID, "base_rev": p.rev(rowID),
	})
}

// itemsOf is the list as this phone would DRAW it: labels in order_key order,
// which is the only ordering either phone ever computes.
func (p *phone) itemsOf(list string) []string {
	p.t.Helper()

	type line struct{ key, label string }

	lines := []line{}
	for _, row := range p.rows {
		if row.deleted || str(row.state, "list_id") != list {
			continue
		}
		lines = append(lines, line{str(row.state, "order_key"), str(row.state, "label")})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].key < lines[j].key })

	labels := make([]string, 0, len(lines))
	for _, l := range lines {
		labels = append(labels, l.label)
	}
	return labels
}

// The shopping list, which is the whole reason this entity exists: two people
// in the same shop, one of them with no signal.
func TestAListIsSharedBetweenTwoPhones(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	list := camille.startsList("Courses")
	camille.adds(list, "Lait", "a")
	camille.adds(list, "Pain", "b")
	oeufs := camille.adds(list, "Œufs", "c")
	camille.sync()

	jordan.pull()
	want := []string{"Lait", "Pain", "Œufs"}
	if got := jordan.itemsOf(list); !slices.Equal(got, want) {
		t.Fatalf("Jordan reads %v, want %v", got, want)
	}

	// Jordan takes the eggs off the shelf.
	jordan.checks(oeufs)
	jordan.sync()
	camille.pull()

	done := camille.doneBy(oeufs)
	if len(done) != 1 || done[0] != jordan.member {
		t.Errorf("Camille sees %v as having taken the eggs, want Jordan alone", done)
	}
}

// Two people tidy the same list at the same time, each offline, and BOTH their
// moves land. That is what a fractional position buys: they wrote different
// fields of different rows, so there is no conflict to resolve.
func TestTwoOfflineReordersBothLand(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	list := camille.startsList("Courses")
	camille.adds(list, "Lait", "a")
	pain := camille.adds(list, "Pain", "b")
	oeufs := camille.adds(list, "Œufs", "c")
	camille.sync()
	jordan.pull()

	// Camille drags the bread to the top; Jordan slots the eggs in between the
	// milk and the bread. Neither has seen the other.
	camille.moves(pain, "0")
	jordan.moves(oeufs, "aV")

	camille.sync()
	jordan.sync()
	camille.pull()

	want := []string{"Pain", "Lait", "Œufs"}
	if got := camille.itemsOf(list); !slices.Equal(got, want) {
		t.Errorf("Camille reads %v, want %v", got, want)
	}
	if got := jordan.itemsOf(list); !slices.Equal(got, want) {
		t.Errorf("Jordan reads %v, want %v", got, want)
	}
}

// And when they move the SAME line, one position holds and the other person is
// told, with the position they asked for kept intact.
func TestMovingTheSameLineTwiceOverIsReceipted(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	list := camille.startsList("Courses")
	lait := camille.adds(list, "Lait", "a")
	camille.adds(list, "Pain", "b")
	camille.sync()
	jordan.pull()

	camille.moves(lait, "z")
	jordan.moves(lait, "0")

	camille.sync()
	answer := only(t, jordan.sync())

	if outcome := str(answer, "outcome"); outcome != "receipted" {
		t.Fatalf("Jordan's move came back %q, want receipted", outcome)
	}
	if code := str(answer, "code"); code != "field_superseded" {
		t.Errorf("code %q, want field_superseded", code)
	}

	// Camille's position held, and it held on BOTH phones.
	want := []string{"Pain", "Lait"}
	if got := jordan.itemsOf(list); !slices.Equal(got, want) {
		t.Errorf("Jordan reads %v, want %v", got, want)
	}

	cards := jordan.waiting()
	if len(cards) != 1 {
		t.Fatalf("Jordan has %d cards waiting, want 1", len(cards))
	}
	mine, _ := cards[0]["mine"].(map[string]any)
	if str(mine, "order_key") != "0" {
		t.Errorf("the card kept %v, want the position Jordan asked for", mine)
	}
}

// Somebody adds a line on the way to the shop; the list was deleted an hour
// ago. Their words come back to them rather than disappearing.
func TestALineAddedToADeletedListComesBackOverHTTP(t *testing.T) {
	a := newTestAPI(t)

	camille := a.newPhone("Camille", "camille")
	camille.found("Maison")
	jordan := a.newPhone("Jordan", "jordan")
	jordan.joins(camille.invites("adult"))

	list := camille.startsList("Courses")
	camille.adds(list, "Lait", "a")
	camille.sync()
	jordan.pull()

	camille.drops("list", list)
	camille.sync()

	// Jordan never saw the deletion.
	jordan.adds(list, "Beurre demi-sel", "b")
	answer := only(t, jordan.sync())

	if code := str(answer, "code"); code != "target_gone" {
		t.Fatalf("outcome %q (%s), want refused target_gone",
			str(answer, "outcome"), code)
	}

	cards := jordan.waiting()
	if len(cards) != 1 {
		t.Fatalf("Jordan has %d cards waiting, want 1", len(cards))
	}
	mine, _ := cards[0]["mine"].(map[string]any)
	if str(mine, "label") != "Beurre demi-sel" {
		t.Errorf("the card kept %v, want the words Jordan typed", mine)
	}
}
