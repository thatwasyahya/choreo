package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// phone is a simulated client: its own device, its own cursor, its own replica
// and its own outbox.
//
// Two of these against one real PostgreSQL is the only honest way to test a
// synchronisation protocol. A single client cannot disagree with anybody, and a
// fake server would only prove that the fake agrees with itself.
//
// It is deliberately a MINIMAL client, mirroring exactly what the real one is
// allowed to do: queue an intent while offline, push it, and replace a local
// row when the revision coming down is higher. No merging — the merge exists
// once, in Go, on the server.
type phone struct {
	t    *testing.T
	api  *api
	name string

	token    string
	deviceID string
	house    string
	member   string

	cursor  int64
	rows    map[string]replicaRow
	notices map[string]map[string]any
	outbox  []map[string]any
}

// replicaRow is one row as this phone currently sees it.
type replicaRow struct {
	rev     int64
	deleted bool
	state   map[string]any
}

// newPhone signs up an account and takes the device that comes with it.
func (a *api) newPhone(name, login string) *phone {
	a.t.Helper()

	token, deviceID, _ := a.signUp(login)

	return &phone{
		t: a.t, api: a, name: name,
		token: token, deviceID: deviceID,
		rows:    map[string]replicaRow{},
		notices: map[string]map[string]any{},
	}
}

// found creates a household and keeps the membership.
func (p *phone) found(name string) {
	p.t.Helper()

	status, body := p.api.do(http.MethodPost, "/v1/households", p.token, map[string]any{
		"name": name, "tz": "Europe/Paris", "display_name": p.name,
	})
	if status != http.StatusCreated {
		p.t.Fatalf("%s founding a household: status %d, body %v", p.name, status, body)
	}

	p.house = str(body, "household_id")
	p.member = str(body, "member_id")
}

// invites mints a code for somebody else to join with.
func (p *phone) invites(role string) string {
	p.t.Helper()

	status, body := p.api.do(http.MethodPost,
		"/v1/households/"+p.house+"/invites", p.token, map[string]any{"role": role})
	if status != http.StatusCreated {
		p.t.Fatalf("%s inviting: status %d, body %v", p.name, status, body)
	}
	return str(body, "code")
}

// joins redeems a code and becomes a member of that household.
func (p *phone) joins(code string) {
	p.t.Helper()

	status, body := p.api.do(http.MethodPost, "/v1/invites/redeem", p.token, map[string]any{
		"code": code, "display_name": p.name,
	})
	if status != http.StatusOK && status != http.StatusCreated {
		p.t.Fatalf("%s joining: status %d, body %v", p.name, status, body)
	}

	p.house = str(body, "household_id")
	p.member = str(body, "member_id")
}

// tablet is the same person on a second device.
//
// It signs in again rather than copying the token: the unit of synchronisation
// is the device, not the person, so it gets its own device id, its own cursor
// and its own outbox — and it shares a member id, which is what makes the same
// chore ticked on both devices one completion rather than two.
func (p *phone) tablet(login string) *phone {
	p.t.Helper()

	status, body := p.api.do(http.MethodPost, "/v1/sessions", "", map[string]any{
		"login": login, "password": "un mot de passe correct",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusOK {
		p.t.Fatalf("signing in a second device: status %d, body %v", status, body)
	}

	return &phone{
		t: p.t, api: p.api, name: p.name + "'s tablet",
		token: str(body, "access"), deviceID: str(body, "device_id"),
		house: p.house, member: p.member,
		rows:    map[string]replicaRow{},
		notices: map[string]map[string]any{},
	}
}

// --- gestures made offline --------------------------------------------------

// queue records an intent. Nothing leaves the phone until sync.
func (p *phone) queue(op map[string]any) {
	p.t.Helper()

	op["op_id"] = uuid.NewString()
	op["entity"] = "task"
	p.outbox = append(p.outbox, op)
}

func (p *phone) creates(title string) string {
	p.t.Helper()

	rowID := uuid.NewString()
	p.queue(map[string]any{
		"kind": "create", "row_id": rowID,
		"fields": map[string]any{"title": title},
	})
	return rowID
}

// renames carries the revision this phone actually had in front of it, which is
// what makes conflict detection causal rather than chronological.
func (p *phone) renames(rowID, title string) {
	p.t.Helper()

	p.queue(map[string]any{
		"kind": "set_fields", "row_id": rowID,
		"fields":    map[string]any{"title": title},
		"base_revs": map[string]any{"title": p.fieldRev(rowID, "title")},
	})
}

// ticks returns the completion id it minted: that id is the deduplication key
// for points, so a test can prove the same tick replayed counts once.
func (p *phone) ticks(rowID string) string {
	p.t.Helper()

	completion := uuid.NewString()
	p.queue(map[string]any{
		"kind": "complete", "row_id": rowID,
		"completion_id": completion,
	})
	return completion
}

// unticks removes exactly what this phone could see. Never "all of them":
// unticking in the kitchen must not erase a completion recorded in the garage.
func (p *phone) unticks(rowID string) {
	p.t.Helper()

	p.queue(map[string]any{
		"kind": "uncomplete", "row_id": rowID,
		"observed": p.doneBy(rowID),
	})
}

func (p *phone) deletes(rowID string) {
	p.t.Helper()

	p.queue(map[string]any{
		"kind": "delete", "row_id": rowID,
		"base_rev": p.rev(rowID),
	})
}

func (p *phone) restores(rowID string) {
	p.t.Helper()

	p.queue(map[string]any{
		"kind": "restore", "row_id": rowID,
		"base_rev": p.rev(rowID),
	})
}

func (p *phone) answers(noticeID, choice string) {
	p.t.Helper()

	p.outbox = append(p.outbox, map[string]any{
		"op_id": uuid.NewString(), "kind": "notice_resolve", "entity": "notice",
		"row_id": noticeID,
		"fields": map[string]any{"choice": choice},
	})
}

// --- the round trip ---------------------------------------------------------

// sync pushes whatever is queued and applies whatever comes back.
//
// Order is fixed and not negotiable in the real client — pull, settle the queue
// locally, push — but one call does both here, which is what the protocol
// asks for: one tap, one call.
func (p *phone) sync() []map[string]any {
	p.t.Helper()

	sending := p.outbox
	if sending == nil {
		sending = []map[string]any{}
	}

	status, body := p.api.do(http.MethodPost, "/v1/households/"+p.house+"/sync", p.token,
		map[string]any{
			// The version this app speaks. Two, since recurring chores joined
			// the vocabulary — and the server still serves 1, which is the
			// rule that lets a phone nobody has updated keep working.
			"protocol": 2, "app_version": "2.0.0",
			"cursor_seq": p.cursor, "limit": 200,
			"ops": sending,
		})
	if status != http.StatusOK {
		p.t.Fatalf("%s syncing: status %d, body %v", p.name, status, body)
	}

	results := maps(body, "results")

	// Settled operations leave the queue. A retryable refusal stays, which is
	// what keeps a deploy-order mistake from poisoning somebody's outbox.
	kept := []map[string]any{}
	settled := map[string]bool{}
	for _, result := range results {
		if str(result, "outcome") == "refused" && str(result, "code") == "unsupported_op" {
			continue
		}
		settled[str(result, "op_id")] = true
	}
	for _, op := range p.outbox {
		if !settled[str(op, "op_id")] {
			kept = append(kept, op)
		}
	}
	p.outbox = kept

	p.absorb(body)
	return results
}

// pull takes what has changed without sending anything.
func (p *phone) pull() {
	p.t.Helper()

	held := p.outbox
	p.outbox = nil
	p.sync()
	p.outbox = held
}

// absorb applies a page: replace the local row when the revision coming down is
// higher, and move the cursor. No merging anywhere — the server sends states
// that are already merged.
func (p *phone) absorb(body map[string]any) {
	p.t.Helper()

	for _, change := range maps(body, "changes") {
		rowID := str(change, "row_id")
		rev := num(change, "rev")
		state, _ := change["state"].(map[string]any)

		if str(change, "entity") == "notice" {
			p.notices[rowID] = state
			continue
		}

		if known, present := p.rows[rowID]; present && rev <= known.rev {
			continue
		}

		deleted, _ := change["deleted"].(bool)
		p.rows[rowID] = replicaRow{rev: rev, deleted: deleted, state: state}
	}

	p.cursor = num(body, "cursor_seq")
}

// --- what this phone believes -----------------------------------------------

func (p *phone) title(rowID string) string {
	p.t.Helper()

	row, present := p.rows[rowID]
	if !present {
		p.t.Fatalf("%s has never heard of row %s", p.name, rowID)
	}
	return str(row.state, "title")
}

func (p *phone) buried(rowID string) bool {
	p.t.Helper()
	return p.rows[rowID].deleted
}

func (p *phone) rev(rowID string) int64 {
	p.t.Helper()
	return p.rows[rowID].rev
}

func (p *phone) fieldRev(rowID, field string) int64 {
	p.t.Helper()

	revs, _ := p.rows[rowID].state["field_revs"].(map[string]any)
	value, _ := revs[field].(float64)
	return int64(value)
}

// doneBy is who this phone sees as having ticked the row, member by member.
func (p *phone) doneBy(rowID string) []string {
	p.t.Helper()

	members := []string{}
	for _, completion := range maps(p.rows[rowID].state, "completions") {
		members = append(members, str(completion, "member_id"))
	}
	return members
}

func (p *phone) completionIDs(rowID string) []string {
	p.t.Helper()

	ids := []string{}
	for _, completion := range maps(p.rows[rowID].state, "completions") {
		ids = append(ids, str(completion, "completion_id"))
	}
	return ids
}

// waiting is the cards addressed to this phone's member and still open.
func (p *phone) waiting() []map[string]any {
	p.t.Helper()

	open := []map[string]any{}
	for id, notice := range p.notices {
		resolved, _ := notice["resolved"].(bool)
		if resolved || str(notice, "for_member") != p.member {
			continue
		}

		card := map[string]any{}
		for key, value := range notice {
			card[key] = value
		}
		card["id"] = id
		open = append(open, card)
	}
	return open
}

// --- small readers ----------------------------------------------------------

func maps(body map[string]any, key string) []map[string]any {
	list, _ := body[key].([]any)

	out := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		if item, ok := raw.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func num(body map[string]any, key string) int64 {
	value, _ := body[key].(float64)
	return int64(value)
}

// outcomes is the per-operation verdict, keyed by operation, for the tests that
// care about every answer in a batch.
func outcomes(results []map[string]any) map[string]string {
	out := map[string]string{}
	for _, result := range results {
		out[str(result, "op_id")] = str(result, "outcome")
	}
	return out
}

// only returns the single result of a one-operation batch.
func only(t *testing.T, results []map[string]any) map[string]any {
	t.Helper()

	if len(results) != 1 {
		t.Fatalf("got %d results, want exactly one: %v", len(results), results)
	}
	return results[0]
}
