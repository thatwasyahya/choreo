package httpapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Nothing secret reaches the logs, and the logs are where secrets go to live
// forever: shipped off the box, kept for months, read by whoever has access to
// the log store rather than to the database.
//
// The test exercises the paths that HOLD a secret — signing up, signing in,
// failing to sign in, renewing, inviting, redeeming, syncing — at debug level,
// because a secret that only leaks when somebody turns the level up still
// leaks.
func TestNoSecretReachesTheLogs(t *testing.T) {
	var logs bytes.Buffer
	a := newTestAPIWriting(t, &logs)

	const password = "un mot de passe correct"

	status, created := a.do(http.MethodPost, "/v1/accounts", "", map[string]any{
		"login": "camille", "password": password,
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})
	if status != http.StatusCreated {
		t.Fatalf("signing up: status %d, body %v", status, created)
	}

	access := str(created, "access")
	refresh := str(created, "refresh")
	recovery := str(created, "recovery_code")

	household := a.household(access, "Maison")

	_, invited := a.do(http.MethodPost, "/v1/households/"+household+"/invites", access,
		map[string]any{"role": "adult"})
	invite := str(invited, "code")

	// A guest joins, so the redemption path is logged too.
	guest, _, guestRecovery := a.signUp("jordan")
	a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": invite, "display_name": "Jordan"})

	// A renewal, which mints a pair and retires another.
	_, renewed := a.do(http.MethodPost, "/v1/sessions/refresh", "",
		map[string]any{"refresh": refresh})
	renewedAccess := str(renewed, "access")
	renewedRefresh := str(renewed, "refresh")

	// A wrong password, because the failing path is the one that logs most.
	a.do(http.MethodPost, "/v1/sessions", "", map[string]any{
		"login": "camille", "password": "pas le bon mot de passe",
		"protocol": 1, "app_version": "1.0.0", "platform": "android",
	})

	// And an ordinary round trip, so the request log has something to say.
	a.do(http.MethodPost, "/v1/households/"+household+"/sync", renewedAccess,
		syncBody(map[string]any{
			"op_id": uuid.NewString(), "kind": "create", "entity": "task",
			"row_id": uuid.NewString(),
			"fields": map[string]any{"title": "Sortir les poubelles"},
		}))

	written := logs.String()
	if strings.TrimSpace(written) == "" {
		t.Fatal("the server logged nothing at all: this test would pass against a mute server")
	}

	// A test that says "the secret is absent" is only worth as much as its
	// proof that the paths holding it were walked at all.
	for _, route := range []string{"/v1/accounts", "/v1/sessions", "/sync"} {
		if !strings.Contains(written, route) {
			t.Errorf("no log line mentions %s: the paths under test were not exercised",
				route)
		}
	}

	secrets := map[string]string{
		"the password":              password,
		"the wrong password":        "pas le bon mot de passe",
		"the access token":          access,
		"the refresh token":         refresh,
		"the renewed access token":  renewedAccess,
		"the renewed refresh token": renewedRefresh,
		"the recovery code":         recovery,
		"the guest's recovery code": guestRecovery,
		"the invite code":           invite,
	}

	for what, secret := range secrets {
		if secret == "" {
			t.Errorf("%s came back empty: the test is not actually checking it", what)
			continue
		}
		if strings.Contains(written, secret) {
			t.Errorf("%s is in the logs", what)
		}
	}
}
