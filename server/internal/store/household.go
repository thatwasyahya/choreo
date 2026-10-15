package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Member is who the caller is inside one household.
type Member struct {
	ID   uuid.UUID
	Role string
}

// Roles. Only the household's creator is ever admin to begin with; an invite
// code cannot grant it, and the database refuses it.
const (
	RoleAdmin = "admin"
	RoleAdult = "adult"
)

// NewHousehold describes a household about to be created.
type NewHousehold struct {
	Name        string
	TZ          string
	AccountID   uuid.UUID
	DisplayName string
}

// CreateHousehold creates a household and its first member, who is admin.
//
// householdID is minted by the CALLER, not here. That keeps every store method
// to the same shape — context, then the tenant — which is what the architecture
// test enforces, and it means the handler can log the id even if the write
// fails.
func (s *Store) CreateHousehold(
	ctx context.Context, householdID uuid.UUID, spec NewHousehold,
) (Member, error) {
	member := Member{ID: uuid.New(), Role: RoleAdmin}

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		const insertHousehold = `INSERT INTO households (id, name, tz) VALUES ($1, $2, $3)`
		if _, err := tx.Exec(ctx, insertHousehold, householdID, spec.Name, spec.TZ); err != nil {
			return fmt.Errorf("store: creating a household: %w", err)
		}

		// The sequence has to be bumped after the row exists: nextSeq locks it.
		seq, err := s.nextSeq(ctx, tx, householdID)
		if err != nil {
			return err
		}

		fields, err := json.Marshal(map[string]string{"display_name": spec.DisplayName})
		if err != nil {
			return fmt.Errorf("store: encoding member fields: %w", err)
		}

		// updated_by is the member itself: nobody else exists yet.
		const insertMember = `INSERT INTO members
		    (id, household_id, account_id, role, rev, created_rev, updated_by, fields)
		    VALUES ($1, $2, $3, $4, $5, $5, $1, $6)`
		if _, err := tx.Exec(ctx, insertMember,
			member.ID, householdID, spec.AccountID, member.Role, seq, fields); err != nil {
			return fmt.Errorf("store: creating the first member: %w", err)
		}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return member, nil
}

// MemberForAccount resolves the caller inside a household.
//
// This runs on EVERY request rather than at token issuance, which is what makes
// removing someone take effect immediately instead of whenever their access
// token happens to expire. It is also the only place that decides whether a
// household exists as far as this caller is concerned: not a member means
// ErrNotMember, which the HTTP layer renders as 404 and never 403.
func (s *Store) MemberForAccount(
	ctx context.Context, householdID, accountID uuid.UUID,
) (Member, error) {
	const q = `SELECT id, role
	             FROM members
	            WHERE household_id = $1
	              AND account_id = $2
	              AND removed_rev IS NULL
	              AND deleted_rev IS NULL`

	var m Member
	err := s.pool.QueryRow(ctx, q, householdID, accountID).Scan(&m.ID, &m.Role)
	switch {
	case errorsIsNoRows(err):
		return Member{}, ErrNotMember
	case err != nil:
		return Member{}, fmt.Errorf("store: resolving a member: %w", err)
	}
	return m, nil
}

// NewInvite describes a code about to be minted.
type NewInvite struct {
	CodeHash []byte
	Role     string
	// ClaimMember, when set, makes this code claim an existing account-less
	// member instead of creating a new one.
	ClaimMember *uuid.UUID
	CreatedBy   uuid.UUID
	ExpiresAt   time.Time
}

// CreateInvite mints an invitation for a household.
//
// When the code claims an existing member, that member must belong to this
// household and must not already have an account — otherwise a leaked code
// would hand over someone else's identity.
func (s *Store) CreateInvite(
	ctx context.Context, householdID uuid.UUID, spec NewInvite,
) (uuid.UUID, error) {
	id := uuid.New()

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if spec.ClaimMember != nil {
			const check = `SELECT account_id IS NULL
			                 FROM members
			                WHERE household_id = $1 AND id = $2
			                  AND removed_rev IS NULL AND deleted_rev IS NULL`

			var claimable bool
			err := tx.QueryRow(ctx, check, householdID, *spec.ClaimMember).Scan(&claimable)
			switch {
			case errorsIsNoRows(err):
				return ErrNotMember
			case err != nil:
				return fmt.Errorf("store: checking a claimable member: %w", err)
			case !claimable:
				return ErrAlreadyMember
			}
		}

		const q = `INSERT INTO invites
		             (id, household_id, code_hash, member_id, role, created_by, expires_at)
		           VALUES ($1, $2, $3, $4, $5, $6, $7)`
		if _, err := tx.Exec(ctx, q, id, householdID, spec.CodeHash,
			spec.ClaimMember, spec.Role, spec.CreatedBy, spec.ExpiresAt); err != nil {
			return fmt.Errorf("store: creating an invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// AcceptInvite joins an account to a household.
//
// The invite is re-checked under a row lock: the lookup that resolved the code
// happened outside this transaction, and two people racing the same code must
// not both get in.
func (s *Store) AcceptInvite(
	ctx context.Context, householdID, inviteID, accountID uuid.UUID, displayName string,
) (Member, error) {
	var member Member

	err := s.withTx(ctx, func(tx pgx.Tx) error {
		const lock = `SELECT member_id, role
		                FROM invites
		               WHERE id = $1 AND household_id = $2
		                 AND redeemed_at IS NULL AND expires_at > now()
		                 FOR UPDATE`

		var claim *uuid.UUID
		err := tx.QueryRow(ctx, lock, inviteID, householdID).Scan(&claim, &member.Role)
		switch {
		case errorsIsNoRows(err):
			return ErrInviteUnknown
		case err != nil:
			return fmt.Errorf("store: locking an invite: %w", err)
		}

		const already = `SELECT EXISTS (SELECT 1 FROM members
		                                 WHERE household_id = $1 AND account_id = $2
		                                   AND removed_rev IS NULL AND deleted_rev IS NULL)`
		var isMember bool
		if err := tx.QueryRow(ctx, already, householdID, accountID).Scan(&isMember); err != nil {
			return fmt.Errorf("store: checking existing membership: %w", err)
		}
		if isMember {
			return ErrAlreadyMember
		}

		seq, err := s.nextSeq(ctx, tx, householdID)
		if err != nil {
			return err
		}

		fields, err := json.Marshal(map[string]string{"display_name": displayName})
		if err != nil {
			return fmt.Errorf("store: encoding member fields: %w", err)
		}

		if claim != nil {
			// Claiming: the chores already assigned to "Lina" stay hers.
			const take = `UPDATE members
			                 SET account_id = $3, rev = $4, updated_by = id,
			                     updated_at = now(),
			                     fields = fields || $5::jsonb
			               WHERE household_id = $1 AND id = $2
			                 AND account_id IS NULL
			                 AND removed_rev IS NULL AND deleted_rev IS NULL`

			tag, err := tx.Exec(ctx, take, householdID, *claim, accountID, seq, fields)
			if err != nil {
				return fmt.Errorf("store: claiming a member: %w", err)
			}
			if tag.RowsAffected() == 0 {
				// Someone claimed it between minting and redeeming.
				return ErrInviteUnknown
			}
			member.ID = *claim
		} else {
			member.ID = uuid.New()

			const create = `INSERT INTO members
			    (id, household_id, account_id, role, rev, created_rev, updated_by, fields)
			    VALUES ($1, $2, $3, $4, $5, $5, $1, $6)`
			if _, err := tx.Exec(ctx, create,
				member.ID, householdID, accountID, member.Role, seq, fields); err != nil {
				return fmt.Errorf("store: adding a member: %w", err)
			}
		}

		const spend = `UPDATE invites
		                  SET redeemed_at = now(), redeemed_by = $3
		                WHERE id = $1 AND household_id = $2`
		if _, err := tx.Exec(ctx, spend, inviteID, householdID, accountID); err != nil {
			return fmt.Errorf("store: spending an invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return member, nil
}
