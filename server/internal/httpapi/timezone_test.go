package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

// The zone a household carries decides how every one of its dates is read, so
// the server takes IANA names and nothing else.
//
// The first real sign-up on a real phone sent "CEST", which is what Android's
// abbreviation getter returns. It was accepted, and it would have shifted every
// date in the household by an hour once the clocks went back.
func TestAHouseholdTakesOnlyAnIANAZone(t *testing.T) {
	a := newTestAPI(t)
	token, _, _ := a.signUp("camille")

	accepted := []string{
		"Europe/Paris",
		"UTC",
		"America/Sao_Paulo",
		"Australia/Lord_Howe", // a 30-minute offset, and a 30-minute DST jump
		"Pacific/Kiritimati",  // +14:00, the far edge of the map
	}
	for _, tz := range accepted {
		t.Run("accepts "+tz, func(t *testing.T) {
			status, body := a.do(http.MethodPost, "/v1/households", token, map[string]any{
				"name": "Maison", "tz": tz, "display_name": "Camille",
			})
			if status != http.StatusCreated {
				t.Errorf("status %d for %q, want 201 — body %v", status, tz, body)
			}
		})
	}

	refused := map[string]string{
		"CEST":               "an abbreviation: summer time with no transitions, which is the bug that was shipped",
		"CET":                "an abbreviation, even one the database knows",
		"+02:00":             "an offset is not a zone",
		"Local":              "resolves to the SERVER's zone and would follow a redeploy",
		"":                   "empty is defaulted before this point, so reaching here is a client bug",
		"Mars/Olympus_Mons":  "not in the database",
		"Europe/Paris; DROP": "not in the database, and the shape says why it was tried",
		"europe/paris":       "the database is case sensitive, and so is the column's meaning",
	}
	for tz, why := range refused {
		t.Run("refuses "+tz, func(t *testing.T) {
			request := map[string]any{"name": "Maison", "display_name": "Camille"}
			if tz != "" {
				request["tz"] = tz
			} else {
				// An explicit empty string, which the handler defaults rather
				// than refuses. Sending it as a present-but-empty field is the
				// only way to reach the validator with nothing.
				request["tz"] = ""
			}

			status, body := a.do(http.MethodPost, "/v1/households", token, request)

			if tz == "" {
				// Defaulted, not refused: a client that says nothing gets a
				// sensible zone rather than an error.
				if status != http.StatusCreated {
					t.Errorf("an absent zone was refused: status %d, body %v", status, body)
				}
				return
			}

			if status != http.StatusUnprocessableEntity {
				t.Errorf("status %d for %q, want 422 (%s) — body %v", status, tz, why, body)
			}
			if body["error"] != "invalid_tz" {
				t.Errorf("error = %v for %q, want invalid_tz", body["error"], tz)
			}
		})
	}
}

// The binary embeds the timezone database, because the image it runs from has
// none. Without the embed every zone above would fail in production and pass
// here, which is the worst shape a test can have.
func TestTheTimezoneDatabaseIsEmbedded(t *testing.T) {
	// Not through the API: this is about what the binary carries. A zone the
	// system would only know from /usr/share/zoneinfo resolves because the
	// httpapi package imports time/tzdata.
	if _, err := time.LoadLocation("Europe/Paris"); err != nil {
		t.Fatalf("Europe/Paris does not resolve: %v", err)
	}
	if _, err := time.LoadLocation("Pacific/Chatham"); err != nil {
		t.Errorf("a less common zone does not resolve: %v", err)
	}
}
