package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/thatwasyahya/choreo/server/internal/obs"
)

// readinessTimeout keeps a stuck database from turning a readiness probe into
// a hanging request.
const readinessTimeout = 2 * time.Second

// handleHealthz answers as long as the process is alive. It deliberately
// touches nothing else: liveness must not fail because a dependency is down,
// or an orchestrator would restart a server that only needed to wait.
func handleHealthz(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version,
		})
	}
}

// handleReadyz answers only when the server can actually serve, which means
// PostgreSQL is reachable.
func handleReadyz(db Pinger, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			// The detail goes to the logs, never to the client: a probe
			// endpoint is unauthenticated, and a driver error names hosts,
			// ports and sometimes users.
			logger.ErrorContext(ctx, "readiness check failed",
				slog.String("request_id", obs.RequestID(ctx)),
				slog.String("error", err.Error()),
			)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"reason": "database unreachable",
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	// The status line is already on the wire, so a failure here cannot be
	// reported to the client. The connection is what breaks, and the access
	// log already records the request.
	_ = json.NewEncoder(w).Encode(body)
}
