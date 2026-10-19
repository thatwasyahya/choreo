package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// raw sends a request and returns the status and the body untouched, for the
// endpoints that do not answer a single JSON object.
func (a *api) raw(method, path, token string) (int, string) {
	a.t.Helper()

	req := httptest.NewRequestWithContext(a.t.Context(), method, path, http.NoBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "192.0.2.10:54321"

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func syncBody(ops ...map[string]any) map[string]any {
	if ops == nil {
		ops = []map[string]any{}
	}
	return map[string]any{
		"protocol": 1, "app_version": "1.0.0",
		"cursor_seq": 0, "limit": 100,
		"ops": ops,
	}
}

func TestSyncRoundTripOverHTTP(t *testing.T) {
	a := newTestAPI(t)

	token, _, _ := a.signUp("camille")
	id := a.household(token, "Maison")

	rowID := uuid.NewString()
	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/sync", token,
		syncBody(map[string]any{
			"op_id": uuid.NewString(), "kind": "create", "entity": "task",
			"row_id": rowID, "fields": map[string]any{"title": "Sortir les poubelles"},
		}))

	if status != http.StatusOK {
		t.Fatalf("status %d, body %v", status, body)
	}

	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("got %d results, want one per operation — %v", len(results), body)
	}
	first, _ := results[0].(map[string]any)
	if first["outcome"] != "applied" {
		t.Errorf("outcome = %v, want applied", first["outcome"])
	}

	// The push came back down in the same round trip: one tap, one call.
	changes, _ := body["changes"].([]any)
	found := false
	for _, raw := range changes {
		change, _ := raw.(map[string]any)
		if change["row_id"] == rowID {
			found = true
		}
	}
	if !found {
		t.Error("the row just created did not come back in the same answer")
	}
}

// "Role insufficient, member removed, device revoked → refused forbidden."
// Membership is checked against a LIVE row on every request, so removal bites
// immediately — and it answers 404, never 403: telling them apart would confirm
// the household exists.
func TestARemovedMemberLosesAccessImmediately(t *testing.T) {
	a := newTestAPI(t)

	owner, _, _ := a.signUp("camille")
	guest, _, _ := a.signUp("jordan")
	id := a.household(owner, "Maison")

	_, invite := a.do(http.MethodPost, "/v1/households/"+id+"/invites", owner,
		map[string]any{"role": "adult"})
	_, joined := a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": str(invite, "code"), "display_name": "Jordan"})

	if status, _ := a.do(http.MethodPost, "/v1/households/"+id+"/sync", guest,
		syncBody()); status != http.StatusOK {
		t.Fatal("the guest could not sync before being removed")
	}

	execSQLFor(t, `UPDATE members SET removed_rev = rev
	                WHERE household_id = $1 AND id = $2`,
		uuid.MustParse(id), uuid.MustParse(str(joined, "member_id")))

	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/sync", guest, syncBody())
	if status != http.StatusNotFound || body["error"] != "not_found" {
		t.Errorf("status %d, body %v, want 404 not_found", status, body)
	}
}

func TestSyncRefusesAnUnsupportedProtocol(t *testing.T) {
	a := newTestAPI(t)

	token, _, _ := a.signUp("camille")
	id := a.household(token, "Maison")

	request := syncBody()
	request["protocol"] = 99

	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/sync", token, request)
	if status != http.StatusUpgradeRequired {
		t.Errorf("status %d, body %v, want 426", status, body)
	}
}

// The stream carries an explicit end line. Once the first byte is out the
// status is already 200, so a client that reads no "end" is the only thing that
// can tell a truncated world from a closed one.
func TestSnapshotStreamsNDJSONWithAnEnd(t *testing.T) {
	a := newTestAPI(t)

	token, _, _ := a.signUp("camille")
	id := a.household(token, "Maison")

	a.do(http.MethodPost, "/v1/households/"+id+"/sync", token,
		syncBody(map[string]any{
			"op_id": uuid.NewString(), "kind": "create", "entity": "task",
			"row_id": uuid.NewString(), "fields": map[string]any{"title": "Poubelles"},
		}))

	status, body := a.raw(http.MethodGet, "/v1/households/"+id+"/snapshot", token)
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}

	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 3 {
		t.Fatalf("got %d lines, want a header, at least one row and an end:\n%s",
			len(lines), body)
	}

	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("the first line is not JSON: %v", err)
	}
	if header["type"] != "header" {
		t.Errorf("the stream does not open with a header: %v", header)
	}
	if _, present := header["cursor_seq"]; !present {
		t.Error("the header carries no cursor: the client cannot store a position")
	}

	var end map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &end); err != nil {
		t.Fatalf("the last line is not JSON: %v", err)
	}
	if end["type"] != "end" {
		t.Errorf("the stream does not close with an end line: %v", end)
	}
}

func TestSnapshotOfAnotherHouseholdIsNotFound(t *testing.T) {
	a := newTestAPI(t)

	owner, _, _ := a.signUp("camille")
	stranger, _, _ := a.signUp("inconnu")
	id := a.household(owner, "Maison")

	status, body := a.raw(http.MethodGet, "/v1/households/"+id+"/snapshot", stranger)
	if status != http.StatusNotFound {
		t.Errorf("status %d, body %s, want 404", status, body)
	}
	if !bytes.Contains([]byte(body), []byte("not_found")) {
		t.Errorf("body = %s, want not_found", body)
	}
}
