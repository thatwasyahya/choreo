package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/httpapi"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// api is the whole server, wired to a real PostgreSQL.
//
// These tests go through HTTP on purpose. The rules that matter here are not in
// the store — "404 and never 403", "device_revoked and not merely 401", "the
// same answer for an unknown login as for a wrong password" — and every one of
// them lives in the handler and the error mapping.
type api struct {
	handler http.Handler
	t       *testing.T
}

func newTestAPI(t *testing.T) *api {
	t.Helper()

	dsn := os.Getenv("CHOREO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHOREO_TEST_DATABASE_URL is unset — run `make test-db`, then `make test-server`")
	}

	ctx := t.Context()

	if err := store.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(db.Close)

	truncate(t, dsn)

	handler := httpapi.New(httpapi.Deps{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics:   obs.NewMetrics(),
		DB:        db,
		Version:   "test",
		Store:     db,
		Verifier:  auth.NewVerifier(4),
		Limits:    httpapi.DefaultLimits(),
		PublicURL: "https://choreo.test",
	})

	return &api{handler: handler, t: t}
}

func truncate(t *testing.T, dsn string) {
	t.Helper()

	ctx := t.Context()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to truncate: %v", err)
	}
	defer pool.Close()

	const list = `SELECT string_agg(format('%I', tablename), ', ')
	                FROM pg_tables
	               WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`

	var tables *string
	if err := pool.QueryRow(ctx, list).Scan(&tables); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	if tables != nil {
		if _, err := pool.Exec(ctx, "TRUNCATE "+*tables+" CASCADE"); err != nil {
			t.Fatalf("truncating: %v", err)
		}
	}
}

// do sends a request and decodes whatever came back.
func (a *api) do(method, path, token string, body any) (int, map[string]any) {
	a.t.Helper()

	var payload io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("encoding the request: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req := httptest.NewRequestWithContext(a.t.Context(), method, path, payload)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// A stable address, so the per-address buckets behave predictably.
	req.RemoteAddr = "192.0.2.10:54321"

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// signUp creates an account and returns its access token.
func (a *api) signUp(login string) (token, deviceID, recovery string) {
	a.t.Helper()

	status, body := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": login, "password": "un mot de passe correct",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusCreated {
		a.t.Fatalf("sign-up for %q: status %d, body %v", login, status, body)
	}

	return str(body, "access"), str(body, "device_id"), str(body, "recovery_code")
}

// household creates one and returns its id.
func (a *api) household(token, name string) string {
	a.t.Helper()

	status, body := a.do(http.MethodPost, "/v1/households", token, map[string]any{
		"name": name, "tz": "Europe/Paris", "display_name": "Admin",
	})
	if status != http.StatusCreated {
		a.t.Fatalf("creating household %q: status %d, body %v", name, status, body)
	}
	return str(body, "household_id")
}

func str(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

// --- sign-up and sign-in ----------------------------------------------------

func TestSignUpReturnsTheRecoveryCodeOnce(t *testing.T) {
	a := newTestAPI(t)

	_, _, recovery := a.signUp("yahya")
	if recovery == "" {
		t.Fatal("no recovery code was returned — it is the only safety net there is")
	}

	// Signing in again must not hand it out a second time.
	_, body := a.do(http.MethodPost, "/v1/sessions", "", map[string]any{
		"login": "yahya", "password": "un mot de passe correct",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if _, present := body["recovery_code"]; present {
		t.Error("the recovery code was returned again on sign-in")
	}
}

func TestDuplicateLoginIsAConflict(t *testing.T) {
	a := newTestAPI(t)
	a.signUp("yahya")

	status, body := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": "yahya", "password": "un autre mot de passe",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusConflict || body["error"] != "login_taken" {
		t.Errorf("status %d, body %v, want 409 login_taken", status, body)
	}
}

func TestWeakPasswordIsRefused(t *testing.T) {
	a := newTestAPI(t)

	status, body := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": "yahya", "password": "court",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusUnprocessableEntity || body["error"] != "weak_password" {
		t.Errorf("status %d, body %v, want 422 weak_password", status, body)
	}
}

// An unknown login and a wrong password must be indistinguishable, or the API
// becomes a directory of who has an account.
func TestUnknownLoginAndWrongPasswordLookIdentical(t *testing.T) {
	a := newTestAPI(t)
	a.signUp("yahya")

	wrongStatus, wrongBody := a.do(http.MethodPost, "/v1/sessions", "", map[string]any{
		"login": "yahya", "password": "pas le bon mot de passe",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	unknownStatus, unknownBody := a.do(http.MethodPost, "/v1/sessions", "", map[string]any{
		"login": "personne", "password": "pas le bon mot de passe",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})

	if wrongStatus != http.StatusUnauthorized || unknownStatus != http.StatusUnauthorized {
		t.Fatalf("statuses %d and %d, want 401 for both", wrongStatus, unknownStatus)
	}
	if wrongBody["error"] != unknownBody["error"] {
		t.Errorf("different answers: %v vs %v", wrongBody, unknownBody)
	}
	if wrongBody["error"] != "invalid_credentials" {
		t.Errorf("error = %v, want invalid_credentials", wrongBody["error"])
	}
}

func TestAuthenticatedRouteNeedsAToken(t *testing.T) {
	a := newTestAPI(t)

	status, body := a.do(http.MethodGet, "/v1/me", "", nil)
	if status != http.StatusUnauthorized || body["error"] != "unauthenticated" {
		t.Errorf("status %d, body %v, want 401 unauthenticated", status, body)
	}
}

// --- households and isolation -----------------------------------------------

func TestMeListsOnlyOwnHouseholds(t *testing.T) {
	a := newTestAPI(t)

	mine, _, _ := a.signUp("camille")
	theirs, _, _ := a.signUp("jordan")

	a.household(mine, "Chez Camille")
	a.household(theirs, "Chez Jordan")

	_, body := a.do(http.MethodGet, "/v1/me", mine, nil)
	households, _ := body["households"].([]any)
	if len(households) != 1 {
		t.Fatalf("got %d households, want 1 — %v", len(households), body)
	}
	first, _ := households[0].(map[string]any)
	if first["name"] != "Chez Camille" {
		t.Errorf("name = %v, want the caller's own household", first["name"])
	}
}

// The rule the whole isolation design rests on: a household one is not a member
// of answers 404, never 403. Anything else is a membership oracle.
func TestAnotherHouseholdIsNotFound(t *testing.T) {
	a := newTestAPI(t)

	owner, _, _ := a.signUp("camille")
	stranger, _, _ := a.signUp("inconnu")

	id := a.household(owner, "Chez Camille")

	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/invites", stranger,
		map[string]any{"role": "adult"})

	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — 403 would confirm the household exists", status)
	}
	if body["error"] != "not_found" {
		t.Errorf("error = %v, want not_found", body["error"])
	}
}

// And a household that does not exist at all gives exactly the same answer.
func TestNonexistentHouseholdIsIndistinguishable(t *testing.T) {
	a := newTestAPI(t)

	token, _, _ := a.signUp("camille")

	real, realBody := a.do(http.MethodPost,
		"/v1/households/00000000-0000-0000-0000-000000000001/invites", token,
		map[string]any{"role": "adult"})

	if real != http.StatusNotFound || realBody["error"] != "not_found" {
		t.Errorf("status %d, body %v, want 404 not_found", real, realBody)
	}
}

// --- invitations ------------------------------------------------------------

func TestInviteAndRedeem(t *testing.T) {
	a := newTestAPI(t)

	admin, _, _ := a.signUp("camille")
	guest, _, _ := a.signUp("jordan")
	id := a.household(admin, "Maison")

	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/invites", admin,
		map[string]any{"role": "adult"})
	if status != http.StatusCreated {
		t.Fatalf("minting an invite: status %d, body %v", status, body)
	}

	code := str(body, "code")
	if code == "" {
		t.Fatal("no code in the response")
	}
	// The QR payload must carry the server address, or a self-hosted household
	// could not invite anyone.
	if qr := str(body, "qr"); !bytes.Contains([]byte(qr), []byte("choreo.test")) {
		t.Errorf("qr = %q, want it to carry the server address", qr)
	}

	joinStatus, joinBody := a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": code, "display_name": "Jordan"})
	if joinStatus != http.StatusOK {
		t.Fatalf("redeeming: status %d, body %v", joinStatus, joinBody)
	}
	if joinBody["household_id"] != id {
		t.Errorf("joined %v, want %v", joinBody["household_id"], id)
	}

	// And now the guest sees the household.
	_, me := a.do(http.MethodGet, "/v1/me", guest, nil)
	households, _ := me["households"].([]any)
	if len(households) != 1 {
		t.Errorf("the guest sees %d households, want 1", len(households))
	}
}

// A code typed back by hand, with its dashes and in the wrong case, has to work.
func TestRedeemForgivesHumanTyping(t *testing.T) {
	a := newTestAPI(t)

	admin, _, _ := a.signUp("camille")
	guest, _, _ := a.signUp("jordan")
	id := a.household(admin, "Maison")

	_, body := a.do(http.MethodPost, "/v1/households/"+id+"/invites", admin,
		map[string]any{"role": "adult"})

	messy := "  " + str(body, "code") + "  "
	status, joinBody := a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": messy, "display_name": "Jordan"})

	if status != http.StatusOK {
		t.Errorf("status %d, body %v — a code with stray spacing must still work",
			status, joinBody)
	}
}

func TestNonAdminCannotInvite(t *testing.T) {
	a := newTestAPI(t)

	admin, _, _ := a.signUp("camille")
	guest, _, _ := a.signUp("jordan")
	id := a.household(admin, "Maison")

	_, body := a.do(http.MethodPost, "/v1/households/"+id+"/invites", admin,
		map[string]any{"role": "adult"})
	a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": str(body, "code"), "display_name": "Jordan"})

	// 403 and not 404 here: the guest already knows they are a member, so
	// there is nothing left to hide. The 404 rule protects membership, not
	// permissions.
	status, refused := a.do(http.MethodPost, "/v1/households/"+id+"/invites", guest,
		map[string]any{"role": "adult"})
	if status != http.StatusForbidden || refused["error"] != "forbidden_role" {
		t.Errorf("status %d, body %v, want 403 forbidden_role", status, refused)
	}
}

func TestInviteCannotGrantAdmin(t *testing.T) {
	a := newTestAPI(t)

	admin, _, _ := a.signUp("camille")
	id := a.household(admin, "Maison")

	status, body := a.do(http.MethodPost, "/v1/households/"+id+"/invites", admin,
		map[string]any{"role": "admin"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status %d, body %v — an invite must never grant admin", status, body)
	}
}

// --- sessions and devices ---------------------------------------------------

// The distinction the client depends on: signing out keeps the local replica,
// having the device removed wipes it.
func TestSignOutAndRevocationAreDifferentAnswers(t *testing.T) {
	a := newTestAPI(t)

	token, _, _ := a.signUp("yahya")

	if status, _ := a.do(http.MethodDelete, "/v1/sessions", token, nil); status != http.StatusNoContent {
		t.Fatalf("signing out: status %d", status)
	}
	status, body := a.do(http.MethodGet, "/v1/me", token, nil)
	if status != http.StatusUnauthorized || body["error"] != "unauthenticated" {
		t.Errorf("after sign-out: status %d, body %v, want 401 unauthenticated", status, body)
	}

	// A second account, this time removing the device.
	other, deviceID, _ := a.signUp("camille")

	if status, _ := a.do(http.MethodDelete, "/v1/devices/"+deviceID, other, nil); status != http.StatusNoContent {
		t.Fatalf("revoking a device: status %d", status)
	}
	status, body = a.do(http.MethodGet, "/v1/me", other, nil)
	if status != http.StatusUnauthorized || body["error"] != "device_revoked" {
		t.Errorf("after revocation: status %d, body %v, want 401 device_revoked", status, body)
	}
}

func TestCannotRevokeSomeoneElsesDevice(t *testing.T) {
	a := newTestAPI(t)

	mine, _, _ := a.signUp("camille")
	_, theirDevice, _ := a.signUp("jordan")

	status, body := a.do(http.MethodDelete, "/v1/devices/"+theirDevice, mine, nil)
	if status != http.StatusNotFound || body["error"] != "not_found" {
		t.Errorf("status %d, body %v, want 404 not_found", status, body)
	}
}

func TestRefreshRotatesAndRetiresTheOldAccess(t *testing.T) {
	a := newTestAPI(t)

	status, created := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": "yahya", "password": "un mot de passe correct",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusCreated {
		t.Fatalf("sign-up: status %d, body %v", status, created)
	}

	oldAccess := str(created, "access")
	refreshed, pair := a.do(http.MethodPost, "/v1/sessions/refresh", "",
		map[string]any{"refresh": str(created, "refresh")})
	if refreshed != http.StatusOK {
		t.Fatalf("refresh: status %d, body %v", refreshed, pair)
	}

	if status, _ := a.do(http.MethodGet, "/v1/me", str(pair, "access"), nil); status != http.StatusOK {
		t.Errorf("the new access token does not work: status %d", status)
	}
	if status, _ := a.do(http.MethodGet, "/v1/me", oldAccess, nil); status != http.StatusUnauthorized {
		t.Errorf("the superseded access token still works: status %d", status)
	}
}

// The lost-response retry: the client never saw the reply and sends the same
// refresh again. Treating that as theft would lock an honest household out.
func TestRefreshRetryWithinGraceSucceeds(t *testing.T) {
	a := newTestAPI(t)

	_, created := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": "yahya", "password": "un mot de passe correct",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	refresh := str(created, "refresh")

	if status, _ := a.do(http.MethodPost, "/v1/sessions/refresh", "",
		map[string]any{"refresh": refresh}); status != http.StatusOK {
		t.Fatalf("first rotation failed")
	}

	status, body := a.do(http.MethodPost, "/v1/sessions/refresh", "",
		map[string]any{"refresh": refresh})
	if status != http.StatusOK {
		t.Errorf("status %d, body %v — a lost-response retry must be forgiven", status, body)
	}
}

// --- recovery ---------------------------------------------------------------

func TestRecoveryReplacesThePasswordAndTheCode(t *testing.T) {
	a := newTestAPI(t)

	_, _, recovery := a.signUp("yahya")

	status, body := a.do(http.MethodPost, "/v1/sessions/recover", "", map[string]any{
		"login": "yahya", "recovery_code": recovery,
		"new_password": "un nouveau mot de passe",
		"protocol":     1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusOK {
		t.Fatalf("recovery: status %d, body %v", status, body)
	}

	if next := str(body, "recovery_code"); next == "" || next == recovery {
		t.Error("recovery must hand out a NEW code and spend the old one")
	}

	// The old recovery code is spent.
	again, _ := a.do(http.MethodPost, "/v1/sessions/recover", "", map[string]any{
		"login": "yahya", "recovery_code": recovery,
		"new_password": "encore un autre mot de passe",
		"protocol":     1, "app_version": "1.0.0", "platform": "android",
	})
	if again != http.StatusUnauthorized {
		t.Errorf("status %d, want 401 — a spent recovery code must not work twice", again)
	}
}

func TestRecoveryRevokesTheOtherDevices(t *testing.T) {
	a := newTestAPI(t)

	oldToken, _, recovery := a.signUp("yahya")

	if status, _ := a.do(http.MethodGet, "/v1/me", oldToken, nil); status != http.StatusOK {
		t.Fatal("the original token did not work to begin with")
	}

	a.do(http.MethodPost, "/v1/sessions/recover", "", map[string]any{
		"login": "yahya", "recovery_code": recovery,
		"new_password": "un nouveau mot de passe",
		"protocol":     1, "app_version": "1.0.0", "platform": "android",
	})

	// The handset someone else may be holding is told to wipe.
	status, body := a.do(http.MethodGet, "/v1/me", oldToken, nil)
	if status != http.StatusUnauthorized || body["error"] != "device_revoked" {
		t.Errorf("status %d, body %v, want 401 device_revoked", status, body)
	}
}
