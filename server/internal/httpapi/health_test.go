package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thatwasyahya/choreo/server/internal/httpapi"
	"github.com/thatwasyahya/choreo/server/internal/obs"
)

type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func newHandler(t *testing.T, db httpapi.Pinger) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: obs.NewMetrics(),
		DB:      db,
		Version: "test",
	})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	rec := get(t, newHandler(t, stubPinger{}), "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"version":"test"`) {
		t.Errorf("body does not carry the version: %s", rec.Body.String())
	}
}

// Liveness must not depend on the database. If it did, an orchestrator would
// restart a server whose only problem is that PostgreSQL is still booting —
// and restarting it would not help.
func TestHealthzStaysOKWhenTheDatabaseIsDown(t *testing.T) {
	h := newHandler(t, stubPinger{err: errors.New("connection refused")})

	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d even with the database down", rec.Code, http.StatusOK)
	}
}

func TestReadyzIsOKWhenTheDatabaseAnswers(t *testing.T) {
	rec := get(t, newHandler(t, stubPinger{}), "/readyz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"ready"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestReadyzFailsWhenTheDatabaseIsDown(t *testing.T) {
	h := newHandler(t, stubPinger{err: errors.New("connection refused")})
	rec := get(t, h, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// /readyz is unauthenticated. A driver error names hosts, ports and sometimes
// users, so it belongs in the logs and nowhere near the response body.
func TestReadyzDoesNotLeakTheDatabaseError(t *testing.T) {
	secret := "postgres://admin:hunter2@10.0.0.7:5432 connection refused"
	h := newHandler(t, stubPinger{err: errors.New(secret)})

	body := get(t, h, "/readyz").Body.String()

	for _, leak := range []string{"hunter2", "10.0.0.7", "admin"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %q: %s", leak, body)
		}
	}
}

// Every response carries a correlation id, and a caller-supplied one is kept
// so a trace survives across hops.
func TestRequestIDIsSetAndHonoured(t *testing.T) {
	h := newHandler(t, stubPinger{})

	rec := get(t, h, "/healthz")
	if rec.Header().Get(obs.HeaderRequestID) == "" {
		t.Error("no request id on the response")
	}

	withID := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	req.Header.Set(obs.HeaderRequestID, "caller-supplied-id")
	h.ServeHTTP(withID, req)

	if got := withID.Header().Get(obs.HeaderRequestID); got != "caller-supplied-id" {
		t.Errorf("request id = %q, want the caller's own", got)
	}
}

func TestMetricsCountRequests(t *testing.T) {
	h := newHandler(t, stubPinger{})

	get(t, h, "/healthz")
	body := get(t, h, "/metrics").Body.String()

	if !strings.Contains(body, "choreo_http_requests_total") {
		t.Fatalf("the request counter is missing from /metrics")
	}
	if !strings.Contains(body, `route="GET /healthz"`) {
		t.Errorf("requests are not labelled by route pattern: %s", firstLines(body, 40))
	}
}

// An unknown path must not mint a new time series, or anyone probing random
// URLs could blow up the registry.
func TestUnmatchedRoutesShareOneLabel(t *testing.T) {
	h := newHandler(t, stubPinger{})

	get(t, h, "/this-does-not-exist")
	get(t, h, "/neither-does-this")

	body := get(t, h, "/metrics").Body.String()
	if !strings.Contains(body, `route="unmatched"`) {
		t.Errorf("unmatched routes are not grouped: %s", firstLines(body, 40))
	}
	for _, path := range []string{"this-does-not-exist", "neither-does-this"} {
		if strings.Contains(body, path) {
			t.Errorf("raw path %q became a metric label", path)
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
