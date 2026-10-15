package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity holds accounts, devices and sessions.
//
// None of it is household-scoped: an account exists before any household and
// can belong to several. That is why it is a separate type — the householdID
// rule that governs Store would be meaningless here, and an architecture test
// knows about this exemption by name.
type Identity struct {
	pool *pgxpool.Pool
}

// rotationGrace is how long a just-rotated refresh token may be exchanged
// again, as long as its successor has not itself been used.
//
// This window exists because of a failure that the strict rule caused: a phone
// on a bad connection sends its refresh, the server rotates, and the RESPONSE
// is lost. The client still holds the old token, retries, and under a strict
// "replay means revoke" rule it locks itself out — which is precisely the
// re-authentication that decision A1 was taken to avoid.
//
// Theft detection survives: replaying a token whose successor has already been
// used is still a revocation, and that is the real signature of two parties
// alternating on one chain. See docs/AUTH.md.
const rotationGrace = 60 * time.Second

// Account is what the server knows about a login.
type Account struct {
	ID           uuid.UUID
	Login        string
	PasswordHash string
	RecoveryHash string
}

// CreateAccount stores a new account and returns its id.
func (i *Identity) CreateAccount(ctx context.Context, login, pwHash, recoveryHash string) (uuid.UUID, error) {
	const q = `INSERT INTO accounts (id, login, pw_hash, recovery_hash)
	           VALUES ($1, $2, $3, $4)`

	id := uuid.New()
	if _, err := i.pool.Exec(ctx, q, id, login, pwHash, recoveryHash); err != nil {
		if isUniqueViolation(err, "accounts_login_key") {
			return uuid.Nil, ErrLoginTaken
		}
		return uuid.Nil, fmt.Errorf("store: creating an account: %w", err)
	}
	return id, nil
}

// AccountByLogin looks an account up for a sign-in attempt.
//
// The caller must still spend the hashing budget when this returns
// ErrAccountUnknown: answering instantly for an unknown login and slowly for a
// known one tells an attacker which logins are real.
func (i *Identity) AccountByLogin(ctx context.Context, login string) (Account, error) {
	const q = `SELECT id, login, pw_hash, recovery_hash FROM accounts WHERE login = $1`

	var a Account
	err := i.pool.QueryRow(ctx, q, login).Scan(&a.ID, &a.Login, &a.PasswordHash, &a.RecoveryHash)
	switch {
	case errorsIsNoRows(err):
		return Account{}, ErrAccountUnknown
	case err != nil:
		return Account{}, fmt.Errorf("store: reading an account: %w", err)
	}
	return a, nil
}

// AccountByID reads an already-authenticated account, to check a current
// password or a recovery code against it.
func (i *Identity) AccountByID(ctx context.Context, accountID uuid.UUID) (Account, error) {
	const q = `SELECT id, login, pw_hash, recovery_hash FROM accounts WHERE id = $1`

	var a Account
	err := i.pool.QueryRow(ctx, q, accountID).Scan(&a.ID, &a.Login, &a.PasswordHash, &a.RecoveryHash)
	switch {
	case errorsIsNoRows(err):
		return Account{}, ErrAccountUnknown
	case err != nil:
		return Account{}, fmt.Errorf("store: reading an account: %w", err)
	}
	return a, nil
}

// SetPassword replaces the password hash, leaving the recovery code alone.
func (i *Identity) SetPassword(ctx context.Context, accountID uuid.UUID, pwHash string) error {
	const q = `UPDATE accounts SET pw_hash = $2 WHERE id = $1`
	return i.exec(ctx, "setting a password", q, accountID, pwHash)
}

// SetPasswordAndRecovery replaces both, which is what recovery does: the old
// recovery code is spent by definition.
func (i *Identity) SetPasswordAndRecovery(ctx context.Context, accountID uuid.UUID, pwHash, recoveryHash string) error {
	const q = `UPDATE accounts SET pw_hash = $2, recovery_hash = $3 WHERE id = $1`
	return i.exec(ctx, "setting a password and recovery code", q, accountID, pwHash, recoveryHash)
}

// RegisterDevice mints a device id for an installation.
//
// The server mints it, never the client: device_id carries the sync cursor, so a
// client able to claim another device's id would corrupt that device's replica.
func (i *Identity) RegisterDevice(
	ctx context.Context, accountID uuid.UUID, appVersion string, protocol int, platform string,
) (uuid.UUID, error) {
	const q = `INSERT INTO devices (id, account_id, app_version, protocol, platform)
	           VALUES ($1, $2, $3, $4, $5)`

	id := uuid.New()
	if _, err := i.pool.Exec(ctx, q, id, accountID, appVersion, protocol, platform); err != nil {
		return uuid.Nil, fmt.Errorf("store: registering a device: %w", err)
	}
	return id, nil
}

// CheckDevice verifies that a device belongs to an account and is live.
func (i *Identity) CheckDevice(ctx context.Context, deviceID, accountID uuid.UUID) error {
	const q = `SELECT revoked_at IS NOT NULL FROM devices WHERE id = $1 AND account_id = $2`

	var revoked bool
	err := i.pool.QueryRow(ctx, q, deviceID, accountID).Scan(&revoked)
	switch {
	case errorsIsNoRows(err):
		// Not ours, or not a device at all: the same answer either way, so
		// nobody can probe for other people's device ids.
		return ErrDeviceUnknown
	case err != nil:
		return fmt.Errorf("store: checking a device: %w", err)
	case revoked:
		return ErrDeviceRevoked
	}
	return nil
}

// RevokeDevice marks a device revoked.
//
// It deliberately leaves the session rows in place, inert. Deleting them would
// make the next request indistinguishable from an ordinary expiry, and the
// difference matters to the client: signing out keeps the local replica,
// "this phone is not mine any more" wipes it. Keeping the row is what lets the
// server answer device_revoked rather than a bare 401. The nightly purge
// collects them.
func (i *Identity) RevokeDevice(ctx context.Context, accountID, deviceID uuid.UUID) error {
	const q = `UPDATE devices SET revoked_at = now()
	            WHERE id = $1 AND account_id = $2 AND revoked_at IS NULL`

	tag, err := i.pool.Exec(ctx, q, deviceID, accountID)
	if err != nil {
		return fmt.Errorf("store: revoking a device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceUnknown
	}
	return nil
}

// RevokeOtherDevices revokes every device of an account except one.
//
// Recovery uses it: the common case is a lost phone, and leaving it signed in
// is the thing nobody wants. Those devices get device_revoked and wipe, which
// is the right outcome for a handset someone else is holding.
func (i *Identity) RevokeOtherDevices(ctx context.Context, accountID, keep uuid.UUID) error {
	const q = `UPDATE devices SET revoked_at = now()
	            WHERE account_id = $1 AND id <> $2 AND revoked_at IS NULL`

	if _, err := i.pool.Exec(ctx, q, accountID, keep); err != nil {
		return fmt.Errorf("store: revoking other devices: %w", err)
	}
	return nil
}

// Session identifies who is calling, and from which installation.
type Session struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	DeviceID  uuid.UUID
}

// NewSession is everything needed to store a freshly minted token pair.
type NewSession struct {
	AccountID    uuid.UUID
	DeviceID     uuid.UUID
	AccessHash   []byte
	RefreshHash  []byte
	AccessUntil  time.Time
	RefreshUntil time.Time
	RotatedFrom  *uuid.UUID
}

// CreateSession stores a token pair.
func (i *Identity) CreateSession(ctx context.Context, s NewSession) (uuid.UUID, error) {
	const q = `INSERT INTO sessions
	             (id, account_id, device_id, access_hash, refresh_hash,
	              access_expires_at, refresh_expires_at, rotated_from)
	           VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	id := uuid.New()
	_, err := i.pool.Exec(ctx, q, id, s.AccountID, s.DeviceID,
		s.AccessHash, s.RefreshHash, s.AccessUntil, s.RefreshUntil, s.RotatedFrom)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: creating a session: %w", err)
	}
	return id, nil
}

// AuthenticateAccess resolves an access token to a session.
//
// Device revocation is checked here rather than cached anywhere, so removing a
// device takes effect on its next request instead of whenever its token happens
// to expire.
//
// It separates ErrDeviceRevoked from ErrSessionUnknown on purpose: the client
// reacts differently to each, and collapsing them would mean a removed phone
// keeps a full copy of the household.
func (i *Identity) AuthenticateAccess(ctx context.Context, digest []byte) (Session, error) {
	const q = `SELECT s.id, s.account_id, s.device_id, d.revoked_at IS NOT NULL
	             FROM sessions s
	             JOIN devices d ON d.id = s.device_id
	            WHERE s.access_hash = $1
	              AND s.access_expires_at > now()
	              AND s.consumed_at IS NULL`

	var (
		s       Session
		revoked bool
	)
	err := i.pool.QueryRow(ctx, q, digest).Scan(&s.ID, &s.AccountID, &s.DeviceID, &revoked)
	switch {
	case errorsIsNoRows(err):
		return Session{}, ErrSessionUnknown
	case err != nil:
		return Session{}, fmt.Errorf("store: authenticating: %w", err)
	case revoked:
		return Session{}, ErrDeviceRevoked
	}
	return s, nil
}

// RotateRefresh exchanges a refresh token for the right to mint a new pair.
//
// It returns the session being replaced, so the caller can store the successor
// with rotated_from pointing at it.
//
// The rules, in order:
//
//   - unknown or expired token: ErrSessionUnknown.
//   - already consumed, and a successor has itself been used: ErrRefreshReused.
//     The caller must revoke the device — two parties are alternating on one
//     chain and we cannot tell which is the owner.
//   - already consumed within the grace window, successor never used: allowed.
//     This is the lost-response retry, and treating it as theft would lock
//     honest people out of their own household.
func (i *Identity) RotateRefresh(ctx context.Context, digest []byte) (Session, error) {
	var out Session

	err := i.inTx(ctx, func(tx pgx.Tx) error {
		// FOR UPDATE: two concurrent retries of the same token must not both
		// be granted a rotation.
		const find = `SELECT s.id, s.account_id, s.device_id,
		                     s.consumed_at, s.refresh_expires_at,
		                     d.revoked_at IS NOT NULL
		                FROM sessions s
		                JOIN devices d ON d.id = s.device_id
		               WHERE s.refresh_hash = $1
		                 FOR UPDATE OF s`

		var (
			consumed  *time.Time
			expires   time.Time
			revoked   bool
			sessionID uuid.UUID
		)
		err := tx.QueryRow(ctx, find, digest).
			Scan(&sessionID, &out.AccountID, &out.DeviceID, &consumed, &expires, &revoked)
		switch {
		case errorsIsNoRows(err):
			return ErrSessionUnknown
		case err != nil:
			return fmt.Errorf("store: finding a refresh token: %w", err)
		case revoked:
			return ErrDeviceRevoked
		case !expires.After(time.Now()):
			return ErrSessionUnknown
		}
		out.ID = sessionID

		if consumed != nil {
			used, err := i.successorUsed(ctx, tx, sessionID)
			if err != nil {
				return err
			}
			if used || time.Since(*consumed) > rotationGrace {
				return ErrRefreshReused
			}

			// Inside the grace window with an unused successor: the client
			// never received the previous reply. Discard that orphan and let a
			// fresh pair be minted.
			const orphan = `DELETE FROM sessions
			                 WHERE rotated_from = $1 AND consumed_at IS NULL`
			if _, err := tx.Exec(ctx, orphan, sessionID); err != nil {
				return fmt.Errorf("store: discarding an unused successor: %w", err)
			}
			return nil
		}

		const consume = `UPDATE sessions SET consumed_at = now() WHERE id = $1`
		if _, err := tx.Exec(ctx, consume, sessionID); err != nil {
			return fmt.Errorf("store: consuming a refresh token: %w", err)
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return out, nil
}

// successorUsed reports whether any successor of this session was itself
// rotated, which proves the client received it.
func (i *Identity) successorUsed(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM sessions
	                           WHERE rotated_from = $1 AND consumed_at IS NOT NULL)`

	var used bool
	if err := tx.QueryRow(ctx, q, sessionID).Scan(&used); err != nil {
		return false, fmt.Errorf("store: checking a successor: %w", err)
	}
	return used, nil
}

// DeleteSession signs one installation out. The local replica is kept: signing
// out is not being wiped.
func (i *Identity) DeleteSession(ctx context.Context, sessionID uuid.UUID) error {
	const q = `DELETE FROM sessions WHERE id = $1`
	return i.exec(ctx, "deleting a session", q, sessionID)
}

// RevokeDeviceSessions drops every session of a device without revoking the
// device itself. Used on refresh reuse.
func (i *Identity) RevokeDeviceSessions(ctx context.Context, deviceID uuid.UUID) error {
	const q = `DELETE FROM sessions WHERE device_id = $1`
	return i.exec(ctx, "dropping device sessions", q, deviceID)
}

// Membership is one household an account belongs to.
type Membership struct {
	HouseholdID uuid.UUID
	MemberID    uuid.UUID
	Name        string
	Role        string
	TZ          string
}

// Memberships lists the live households of an account.
//
// Cross-household by nature: listing them is the point. Only live rows are
// returned, so a member who was removed sees the household disappear on their
// next call rather than when their token expires.
func (i *Identity) Memberships(ctx context.Context, accountID uuid.UUID) ([]Membership, error) {
	const q = `SELECT h.id, m.id, h.name, m.role, h.tz
	             FROM members m
	             JOIN households h ON h.id = m.household_id
	            WHERE m.account_id = $1
	              AND m.removed_rev IS NULL
	              AND m.deleted_rev IS NULL
	         ORDER BY h.created_at`

	rows, err := i.pool.Query(ctx, q, accountID)
	if err != nil {
		return nil, fmt.Errorf("store: listing memberships: %w", err)
	}
	defer rows.Close()

	out := make([]Membership, 0, 2)
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.HouseholdID, &m.MemberID, &m.Name, &m.Role, &m.TZ); err != nil {
			return nil, fmt.Errorf("store: scanning a membership: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading memberships: %w", err)
	}
	return out, nil
}

// Invite is a redeemable code, resolved to the household it opens.
type Invite struct {
	ID          uuid.UUID
	HouseholdID uuid.UUID
	MemberID    *uuid.UUID // non-nil = claims an existing account-less member
	Role        string
}

// InviteByCode resolves a code.
//
// This is the one deliberate cross-tenant read in the whole store: an invite
// code is a global credential, like a password, so it has to be looked up
// without knowing the household first. Everything the caller does afterwards is
// household-scoped.
//
// Unknown, expired and already-used all return ErrInviteUnknown: three
// different answers would be an oracle.
func (i *Identity) InviteByCode(ctx context.Context, digest []byte) (Invite, error) {
	const q = `SELECT id, household_id, member_id, role
	             FROM invites
	            WHERE code_hash = $1
	              AND redeemed_at IS NULL
	              AND expires_at > now()`

	var inv Invite
	err := i.pool.QueryRow(ctx, q, digest).
		Scan(&inv.ID, &inv.HouseholdID, &inv.MemberID, &inv.Role)
	switch {
	case errorsIsNoRows(err):
		return Invite{}, ErrInviteUnknown
	case err != nil:
		return Invite{}, fmt.Errorf("store: resolving an invite: %w", err)
	}
	return inv, nil
}

func (i *Identity) exec(ctx context.Context, what, query string, args ...any) error {
	if _, err := i.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("store: %s: %w", what, err)
	}
	return nil
}

func (i *Identity) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: beginning a transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: committing: %w", err)
	}
	return nil
}
