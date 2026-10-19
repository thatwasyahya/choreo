package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/httpapi"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
	"github.com/thatwasyahya/choreo/server/internal/wake"
)

// newWakingAPI builds the server with a live notification hub, which the other
// tests deliberately do without.
func newWakingAPI(t *testing.T) *api {
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

	hub := wake.New(dsn, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	go hub.Run(ctx)

	handler := httpapi.New(httpapi.Deps{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics:   obs.NewMetrics(),
		DB:        db,
		Version:   "test",
		Store:     db,
		Verifier:  auth.NewVerifier(4),
		Limits:    httpapi.DefaultLimits(),
		PublicURL: "https://choreo.test",
		Wake:      hub,
	})

	return &api{handler: handler, t: t}
}

func longPoll(ms int) map[string]any {
	body := syncBody()
	body["wait_ms"] = ms
	return body
}

// A parked call must come back as soon as somebody else writes. Without this
// the shared shopping list is not "near real time", it is "whenever the next
// poll happens to land".
func TestLongPollWakesOnAnotherMembersWrite(t *testing.T) {
	a := newWakingAPI(t)

	owner, _, _ := a.signUp("camille")
	guest, _, _ := a.signUp("jordan")
	id := a.household(owner, "Maison")

	_, invite := a.do(http.MethodPost, "/v1/households/"+id+"/invites", owner,
		map[string]any{"role": "adult"})
	a.do(http.MethodPost, "/v1/invites/redeem", guest,
		map[string]any{"code": str(invite, "code"), "display_name": "Jordan"})

	type answer struct {
		status int
		body   map[string]any
		took   time.Duration
	}
	done := make(chan answer, 1)

	go func() {
		started := time.Now()
		// Twenty seconds, as the protocol asks. Waking must take far less.
		status, body := a.do(http.MethodPost, "/v1/households/"+id+"/sync", guest,
			longPoll(20_000))
		done <- answer{status: status, body: body, took: time.Since(started)}
	}()

	// Keep writing until the parked call comes back. One write might land
	// before it has subscribed; the loop makes the test about whether waking
	// works, not about winning a race with the scheduler.
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case got := <-done:
			if got.status != http.StatusOK {
				t.Fatalf("status %d, body %v, want 200 with the change", got.status, got.body)
			}
			if got.took > 15*time.Second {
				t.Errorf("woke after %v — that is the timeout firing, not a notification",
					got.took)
			}
			changes, _ := got.body["changes"].([]any)
			if len(changes) == 0 {
				t.Error("woke with nothing to show")
			}
			return

		case <-ticker.C:
			a.do(http.MethodPost, "/v1/households/"+id+"/sync", owner,
				syncBody(map[string]any{
					"op_id": uuid.NewString(), "kind": "create", "entity": "task",
					"row_id": uuid.NewString(),
					"fields": map[string]any{"title": "Lait"},
				}))

		case <-deadline:
			t.Fatal("the parked call never woke")
		}
	}
}

// Nothing happened: 204, and the head in a header so the client can tell
// "nothing new" from "I am behind" without a body to parse.
func TestLongPollTimesOutWithTheHead(t *testing.T) {
	a := newWakingAPI(t)

	token, _, _ := a.signUp("camille")
	id := a.household(token, "Maison")

	// Catch up first, so there is genuinely nothing to send.
	_, caught := a.do(http.MethodPost, "/v1/households/"+id+"/sync", token, syncBody())

	request := longPoll(300)
	request["cursor_seq"] = caught["cursor_seq"]

	status, header, _ := a.withHeaders(http.MethodPost, "/v1/households/"+id+"/sync",
		token, request)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if header.Get("Choreo-Head-Seq") == "" {
		t.Error("no Choreo-Head-Seq: the client cannot tell whether it is behind")
	}
}

// The claim the cost ceiling rests on: a parked call holds a goroutine and
// NOTHING of the database. If this ever regresses, a household of four with the
// app open starts pinning connections, and the 4 GB machine of decision D-005
// stops being enough.
func TestParkedCallsHoldNoDatabaseConnection(t *testing.T) {
	a := newWakingAPI(t)

	token, _, _ := a.signUp("camille")
	id := a.household(token, "Maison")

	_, caught := a.do(http.MethodPost, "/v1/households/"+id+"/sync", token, syncBody())

	const parked = 6

	var wg sync.WaitGroup
	for range parked {
		wg.Add(1)
		go func() {
			defer wg.Done()

			request := longPoll(1500)
			request["cursor_seq"] = caught["cursor_seq"]
			a.do(http.MethodPost, "/v1/households/"+id+"/sync", token, request)
		}()
	}

	// Sampled while they are all still parked.
	time.Sleep(600 * time.Millisecond)

	var inTransaction int
	execScalar(t, &inTransaction,
		`SELECT count(*) FROM pg_stat_activity
		  WHERE datname = current_database()
		    AND state = 'idle in transaction'`)

	wg.Wait()

	if inTransaction != 0 {
		t.Errorf("%d connections sat idle in transaction while %d calls were parked — "+
			"a parked call must cost a goroutine, not a connection",
			inTransaction, parked)
	}
}
