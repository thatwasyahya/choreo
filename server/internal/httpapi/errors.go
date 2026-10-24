package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/obs"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// Error codes. They are localisation keys, never sentences — the same rule as
// the code column on notices. A server that returns prose decides the wording
// for every client forever, and gets it wrong in both languages.
const (
	codeInternal        = "internal"
	codeUnauthenticated = "unauthenticated"
	codeDeviceRevoked   = "device_revoked"
	codeTokenReuse      = "token_reuse"
	// Not a credential: a localisation key the client looks up.
	codeBadCredentials = "invalid_credentials" //nolint:gosec // G101 false positive
	codeNotFound       = "not_found"
	codeLoginTaken     = "login_taken"
	codeWeakPassword   = "weak_password"
	codeInvalidLogin   = "invalid_login"
	codeInviteUnknown  = "invite_unknown"
	codeAlreadyMember  = "already_member"
	codeRateLimited    = "rate_limited"
	codeMalformed      = "malformed"
	codeInvalidTZ      = "invalid_tz"
	codeForbiddenRole  = "forbidden_role"
)

// errorBody is the one shape every failure takes.
type errorBody struct {
	Error string `json:"error"`
}

// writeError maps an internal error onto a status and a code.
//
// Two rules hold here, and both exist so the response says nothing it should
// not:
//
//   - A household one is not a member of is 404, never 403. Distinguishing them
//     would be a membership oracle, and every id is a UUID precisely so that a
//     guess reveals nothing.
//   - The underlying message goes to the log and never to the client. A driver
//     error names hosts, ports and sometimes accounts.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status, code := classify(err)

	if status >= http.StatusInternalServerError {
		// Only an unmapped error is worth a stack-shaped log line; the rest are
		// ordinary outcomes, already visible in the access log.
		logger.ErrorContext(r.Context(), "unhandled error",
			slog.String("request_id", obs.RequestID(r.Context())),
			slog.String("route", r.Method+" "+r.URL.Path),
			slog.String("error", err.Error()),
		)
	}

	writeJSON(w, status, errorBody{Error: code})
}

func classify(err error) (int, string) {
	switch {
	// --- not found, deliberately indistinguishable from not permitted ---
	case errors.Is(err, store.ErrNotMember),
		errors.Is(err, store.ErrHouseholdUnknown),
		errors.Is(err, store.ErrDeviceUnknown):
		return http.StatusNotFound, codeNotFound

	case errors.Is(err, store.ErrInviteUnknown):
		return http.StatusNotFound, codeInviteUnknown

	// --- authentication ---
	case errors.Is(err, store.ErrSessionUnknown):
		return http.StatusUnauthorized, codeUnauthenticated

	case errors.Is(err, store.ErrDeviceRevoked):
		// The client wipes its local replica on this one, and only this one.
		return http.StatusUnauthorized, codeDeviceRevoked

	case errors.Is(err, store.ErrRefreshReused):
		return http.StatusUnauthorized, codeTokenReuse

	case errors.Is(err, store.ErrAccountUnknown):
		// An unknown login and a wrong password give the same answer.
		return http.StatusUnauthorized, codeBadCredentials

	// --- conflicts ---
	case errors.Is(err, store.ErrLoginTaken):
		return http.StatusConflict, codeLoginTaken

	case errors.Is(err, store.ErrAlreadyMember):
		return http.StatusConflict, codeAlreadyMember

	// --- validation ---
	case errors.Is(err, auth.ErrPasswordShort):
		return http.StatusUnprocessableEntity, codeWeakPassword

	case errors.Is(err, auth.ErrLoginLength),
		errors.Is(err, auth.ErrLoginCharset),
		errors.Is(err, auth.ErrLoginStart):
		return http.StatusUnprocessableEntity, codeInvalidLogin

	default:
		return http.StatusInternalServerError, codeInternal
	}
}
