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
	"time"

	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// Token lifetimes, from docs/AUTH.md.
//
// The access token travels on every request and may end up in an intermediary's
// log; the refresh travels rarely. 180 days and not 60 because the threat model
// is a lost phone — answered by revoking the device, not by expiry — and a wall
// tablet nobody touches must not log itself out (decision D-021, A1).
const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 180 * 24 * time.Hour
)

// Pinger is the slice of the store that the API needs in order to answer
// readiness. It is declared here, where it is consumed, so the HTTP layer
// never depends on the concrete store.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Limits holds one bucket per route that an unauthenticated stranger can reach.
// See docs/AUTH.md for the numbers and for why they live in memory.
type Limits struct {
	SignIn   *auth.Limiter // per login
	SignInIP *auth.Limiter // per address
	SignUp   *auth.Limiter // per address
	Redeem   *auth.Limiter // per account
	Recover  *auth.Limiter // per login
}

// DefaultLimits returns the buckets specified in docs/AUTH.md.
func DefaultLimits() Limits {
	return Limits{
		SignIn:   auth.NewLimiter(10, 15*time.Minute),
		SignInIP: auth.NewLimiter(60, 15*time.Minute),
		SignUp:   auth.NewLimiter(5, time.Hour),
		Redeem:   auth.NewLimiter(10, time.Hour),
		Recover:  auth.NewLimiter(5, time.Hour),
	}
}

// Deps is everything the API needs from the outside. No global state, no
// package-level singleton: the whole handler is built from this.
type Deps struct {
	Logger  *slog.Logger
	Metrics *obs.Metrics
	DB      Pinger
	Version string

	// Store and Verifier carry the authenticated API. With Store nil, only the
	// probes are served — which is what the health tests want, and it keeps
	// them from needing a database.
	Store    *store.Store
	Verifier *auth.Verifier
	Limits   Limits

	// PublicURL is the address clients reach this server at. It goes into the
	// join link behind an invitation QR code, and it cannot be derived from the
	// request: a Host header is set by the caller, and a household running its
	// own instance must be able to invite people to ITS address.
	PublicURL string
}

// handlers carries what the authenticated routes need.
type handlers struct {
	logger    *slog.Logger
	store     *store.Store
	verifier  *auth.Verifier
	limits    Limits
	publicURL string
}

// New builds the HTTP handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /healthz", handleHealthz(d.Version))
	mux.Handle("GET /readyz", handleReadyz(d.DB, d.Logger))
	mux.Handle("GET /metrics", d.Metrics.Handler())

	if d.Store != nil {
		h := &handlers{
			logger:    d.Logger,
			store:     d.Store,
			verifier:  d.Verifier,
			limits:    d.Limits,
			publicURL: d.PublicURL,
		}
		h.routes(mux)
	}

	return obs.Instrument(d.Logger, d.Metrics, mux)
}

// routes registers the authenticated API. Every path is listed here and
// nowhere else, so the surface of the server is one screen long.
func (h *handlers) routes(mux *http.ServeMux) {
	// Identity — reachable without a token.
	mux.HandleFunc("POST /v1/accounts", h.createAccount)
	mux.HandleFunc("POST /v1/sessions", h.signIn)
	mux.HandleFunc("POST /v1/sessions/refresh", h.refresh)
	mux.HandleFunc("POST /v1/sessions/recover", h.recoverAccount)

	// Identity — authenticated.
	mux.HandleFunc("POST /v1/accounts/password", h.requireAuth(h.changePassword))
	mux.HandleFunc("DELETE /v1/sessions", h.requireAuth(h.signOut))
	mux.HandleFunc("GET /v1/me", h.requireAuth(h.me))
	mux.HandleFunc("DELETE /v1/devices/{id}", h.requireAuth(h.revokeDevice))

	// Households.
	mux.HandleFunc("POST /v1/households", h.requireAuth(h.createHousehold))
	mux.HandleFunc("POST /v1/invites/redeem", h.requireAuth(h.redeemInvite))
	mux.HandleFunc("POST /v1/households/{hid}/invites", h.requireMember(h.createInvite))

	// The protocol itself.
	mux.HandleFunc("POST /v1/households/{hid}/sync", h.requireMember(h.sync))
	mux.HandleFunc("GET /v1/households/{hid}/snapshot", h.requireMember(h.snapshot))
}
