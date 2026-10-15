package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Sentinel errors the HTTP layer maps onto status codes. They exist so a
// handler never has to inspect a driver error, and so no PostgreSQL message
// ever reaches a client: those name hosts, ports and sometimes accounts.
//
// Note what is deliberately absent: there is no ErrForbidden. A household one
// is not a member of answers ErrNotMember, which the HTTP layer renders as 404.
// No membership oracle — see docs/SYNC_PROTOCOL.md, isolation, measure 3.
var (
	ErrHouseholdUnknown = errors.New("store: unknown household")
	ErrNotMember        = errors.New("store: not a member of this household")

	ErrLoginTaken     = errors.New("store: login already taken")
	ErrAccountUnknown = errors.New("store: unknown account")

	ErrDeviceUnknown = errors.New("store: unknown device")
	ErrDeviceRevoked = errors.New("store: device revoked")

	ErrSessionUnknown = errors.New("store: unknown or expired session")
	// ErrRefreshReused means a consumed refresh token was presented again.
	// Either the thief or the victim is using it and we cannot tell which, so
	// the caller must revoke the whole device.
	ErrRefreshReused = errors.New("store: refresh token reused")

	ErrInviteUnknown = errors.New("store: unknown, expired or already used invite")
	ErrAlreadyMember = errors.New("store: already a member of this household")
)

// uniqueViolation is the PostgreSQL class for a broken unique constraint.
const uniqueViolation = "23505"

func errorsIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// isUniqueViolation reports whether err is a unique constraint failure, and on
// which constraint.
//
// Matching on the constraint name rather than just the class matters: a login
// collision and a token collision are the same SQL error class but completely
// different answers to the user.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
