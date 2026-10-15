// Package httpapi wires the HTTP surface of the server.
//
// Routing uses net/http from the standard library (decision D-012): the method
// and path patterns available since Go 1.22 cover everything this API needs,
// and that is one dependency fewer to audit.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/thatwasyahya/choreo/server/internal/obs"
)

// Pinger is the slice of the store that the API needs in order to answer
// readiness. It is declared here, where it is consumed, so the HTTP layer
// never depends on the concrete store.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps is everything the API needs from the outside. No global state, no
// package-level singleton: the whole handler is built from this.
type Deps struct {
	Logger  *slog.Logger
	Metrics *obs.Metrics
	DB      Pinger
	Version string
}

// New builds the HTTP handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /healthz", handleHealthz(d.Version))
	mux.Handle("GET /readyz", handleReadyz(d.DB, d.Logger))
	mux.Handle("GET /metrics", d.Metrics.Handler())

	return obs.Instrument(d.Logger, d.Metrics, mux)
}
