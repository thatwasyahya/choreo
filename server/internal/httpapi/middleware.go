package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

type sessionKey struct{}

// callerKey is the zero value of an unexported empty struct: a context key no
// other package can collide with.
//
//nolint:gochecknoglobals // immutable sentinel, never assigned to
var callerKey sessionKey

// maxBodyBytes caps every request body. Without it, a single client can make
// the server allocate until it dies — and the cost ceiling of D-005 assumes
// nobody can.
const maxBodyBytes = 1 << 20 // 1 MiB

// caller returns the authenticated session, which requireAuth guarantees is
// present on any handler it wraps.
func caller(ctx context.Context) store.Session {
	s, _ := ctx.Value(callerKey).(store.Session)
	return s
}

// requireAuth resolves the bearer token, or refuses the request.
//
// The token is resolved against the database on every single request rather
// than trusted from its own contents. That is the whole reason tokens here are
// opaque instead of JWT: removing a device has to take effect now, not when a
// signature happens to expire.
func (h *handlers) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeUnauthenticated})
			return
		}

		session, err := h.store.Identity.AuthenticateAccess(r.Context(), auth.TokenDigest(token))
		if err != nil {
			writeError(w, r, h.logger, err)
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, session)))
	}
}

// requireMember resolves the caller's membership of the household in the path.
//
// It runs on every request, against a live members row — not at token issuance.
// Someone removed from a household stops reading and writing on their next
// call, without waiting for a token to expire.
func (h *handlers) requireMember(
	next func(http.ResponseWriter, *http.Request, uuid.UUID, store.Member),
) http.HandlerFunc {
	return h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		householdID, err := uuid.Parse(r.PathValue("hid"))
		if err != nil {
			// An unparseable id is not a household we are a member of, and it
			// gets the same answer as one that exists and is not ours.
			writeJSON(w, http.StatusNotFound, errorBody{Error: codeNotFound})
			return
		}

		member, err := h.store.MemberForAccount(r.Context(), householdID, caller(r.Context()).AccountID)
		if err != nil {
			writeError(w, r, h.logger, err)
			return
		}

		next(w, r, householdID, member)
	})
}

// bearer extracts the token from the Authorization header.
func bearer(r *http.Request) (string, bool) {
	const prefix = "Bearer "

	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// decode reads a JSON request body, bounded, answering 400 itself on failure.
//
// It returns a bool rather than an error because there is only ever one thing
// to do about a body that will not parse, and a caller that cannot forget to
// answer is better than a sentinel error everyone has to remember to map.
//
// Unknown fields are rejected: a client sending app_versionn deserves to be
// told, not to have its typo silently default to empty.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: codeMalformed})
		return false
	}
	return true
}

// allow charges a rate-limit bucket and answers 429 when it is empty.
func allow(w http.ResponseWriter, limiter *auth.Limiter, key string) bool {
	if limiter.Allow(key) {
		return true
	}
	writeJSON(w, http.StatusTooManyRequests, errorBody{Error: codeRateLimited})
	return false
}

// clientKey identifies a caller for rate limiting when there is no account yet.
//
// RemoteAddr and not a forwarded header: this server sits behind our own Caddy
// and nothing else, so a header a client can set is a header a client can
// forge, and forging it would bypass the bucket entirely.
func clientKey(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		addr = addr[:i]
	}
	return addr
}
