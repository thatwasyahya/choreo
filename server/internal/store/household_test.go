package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

func household(t *testing.T, s *store.Store, accountID uuid.UUID, name string) (uuid.UUID, store.Member) {
	t.Helper()

	id := uuid.New()
	member, err := s.CreateHousehold(t.Context(), id, store.NewHousehold{
		Name:        name,
		TZ:          "Europe/Paris",
		AccountID:   accountID,
		DisplayName: name + " admin",
	})
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	return id, member
}

func TestCreatorIsAdminAndSeesTheHousehold(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	id, member := household(t, s, acc, "Maison")

	if member.Role != store.RoleAdmin {
		t.Errorf("role = %q, want %q", member.Role, store.RoleAdmin)
	}

	memberships, err := s.Identity.Memberships(t.Context(), acc)
	if err != nil {
		t.Fatalf("Memberships: %v", err)
	}
	if len(memberships) != 1 {
		t.Fatalf("got %d memberships, want 1", len(memberships))
	}
	if memberships[0].HouseholdID != id || memberships[0].MemberID != member.ID {
		t.Errorf("membership points at the wrong rows: %+v", memberships[0])
	}
}

// The 404 path. Someone who is not a member must not be able to tell a
// household they cannot see from one that does not exist.
func TestNonMemberIsNotAMember(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	stranger := account(t, s, "inconnu")
	id, _ := household(t, s, owner, "Maison")

	if _, err := s.MemberForAccount(t.Context(), id, stranger); !errors.Is(err, store.ErrNotMember) {
		t.Errorf("err = %v, want ErrNotMember", err)
	}

	// And a household that does not exist gives the same answer.
	if _, err := s.MemberForAccount(t.Context(), uuid.New(), stranger); !errors.Is(err, store.ErrNotMember) {
		t.Errorf("err = %v, want ErrNotMember for an unknown household too", err)
	}
}

// Each household owns its own clock. One household's activity must never move
// another's cursor.
func TestSequencesAreIndependentPerHousehold(t *testing.T) {
	s := newTestStore(t)

	first := account(t, s, "camille")
	second := account(t, s, "jordan")

	a, memberA := household(t, s, first, "Foyer A")
	b, memberB := household(t, s, second, "Foyer B")

	if a == b {
		t.Fatal("two households got the same id")
	}

	var revA, revB int64
	rowA := queryRow(t, `SELECT rev FROM members WHERE household_id = $1 AND id = $2`, a, memberA.ID)
	rowB := queryRow(t, `SELECT rev FROM members WHERE household_id = $1 AND id = $2`, b, memberB.ID)
	revA, revB = rowA, rowB

	// Both are the first write in their own household, so both are 1 — which is
	// the whole point: B did not inherit A's position.
	if revA != 1 || revB != 1 {
		t.Errorf("revA = %d, revB = %d, want 1 and 1", revA, revB)
	}
}

// --- invitations ------------------------------------------------------------

func invite(t *testing.T, s *store.Store, householdID, by uuid.UUID, claim *uuid.UUID) []byte {
	t.Helper()

	_, digest, err := auth.NewInviteCode()
	if err != nil {
		t.Fatalf("NewInviteCode: %v", err)
	}

	if _, err := s.CreateInvite(t.Context(), householdID, store.NewInvite{
		CodeHash:    digest,
		Role:        store.RoleAdult,
		ClaimMember: claim,
		CreatedBy:   by,
		ExpiresAt:   time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	return digest
}

func TestInviteAddsAMember(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	guest := account(t, s, "jordan")
	id, admin := household(t, s, owner, "Maison")

	digest := invite(t, s, id, admin.ID, nil)

	inv, err := s.Identity.InviteByCode(t.Context(), digest)
	if err != nil {
		t.Fatalf("InviteByCode: %v", err)
	}
	if inv.HouseholdID != id {
		t.Fatalf("the code resolved to the wrong household")
	}

	member, err := s.AcceptInvite(t.Context(), id, inv.ID, guest, "Jordan")
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if member.Role != store.RoleAdult {
		t.Errorf("role = %q, want %q", member.Role, store.RoleAdult)
	}

	if _, err := s.MemberForAccount(t.Context(), id, guest); err != nil {
		t.Errorf("the new member cannot see the household: %v", err)
	}
}

func TestInviteIsSingleUse(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	first := account(t, s, "jordan")
	second := account(t, s, "ines")
	id, admin := household(t, s, owner, "Maison")

	digest := invite(t, s, id, admin.ID, nil)
	inv, err := s.Identity.InviteByCode(t.Context(), digest)
	if err != nil {
		t.Fatalf("InviteByCode: %v", err)
	}

	if _, err := s.AcceptInvite(t.Context(), id, inv.ID, first, "Jordan"); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	if _, err := s.AcceptInvite(t.Context(), id, inv.ID, second, "Inès"); !errors.Is(err, store.ErrInviteUnknown) {
		t.Errorf("err = %v, want ErrInviteUnknown on second use", err)
	}

	// And the code no longer resolves at all.
	if _, err := s.Identity.InviteByCode(t.Context(), digest); !errors.Is(err, store.ErrInviteUnknown) {
		t.Errorf("err = %v, want ErrInviteUnknown for a spent code", err)
	}
}

func TestExpiredInviteIsSimplyUnknown(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	id, admin := household(t, s, owner, "Maison")
	digest := invite(t, s, id, admin.ID, nil)

	execSQL(t, `UPDATE invites SET expires_at = now() - interval '1 hour' WHERE code_hash = $1`, digest)

	if _, err := s.Identity.InviteByCode(t.Context(), digest); !errors.Is(err, store.ErrInviteUnknown) {
		t.Errorf("err = %v, want ErrInviteUnknown — unknown, expired and spent must look alike", err)
	}
}

func TestAlreadyAMemberCannotRedeem(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	id, admin := household(t, s, owner, "Maison")
	digest := invite(t, s, id, admin.ID, nil)

	inv, err := s.Identity.InviteByCode(t.Context(), digest)
	if err != nil {
		t.Fatalf("InviteByCode: %v", err)
	}

	if _, err := s.AcceptInvite(t.Context(), id, inv.ID, owner, "Camille"); !errors.Is(err, store.ErrAlreadyMember) {
		t.Errorf("err = %v, want ErrAlreadyMember", err)
	}
}

// Episode E02: the chores already assigned to Lina stay hers once Lina finally
// has a phone. The member id must survive the claim.
func TestClaimingKeepsTheSameMember(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	lina := account(t, s, "lina")
	id, admin := household(t, s, owner, "Maison")

	// An account-less member, as the household would have created for a child.
	linaMember := uuid.New()
	execSQL(t, `INSERT INTO members (id, household_id, account_id, role, rev, created_rev, updated_by, fields)
	            VALUES ($1, $2, NULL, 'child', 2, 2, $1, '{"display_name":"Lina"}')`,
		linaMember, id)

	digest := invite(t, s, id, admin.ID, &linaMember)
	inv, err := s.Identity.InviteByCode(t.Context(), digest)
	if err != nil {
		t.Fatalf("InviteByCode: %v", err)
	}
	if inv.MemberID == nil || *inv.MemberID != linaMember {
		t.Fatalf("the invite does not carry the member it claims: %+v", inv.MemberID)
	}

	claimed, err := s.AcceptInvite(t.Context(), id, inv.ID, lina, "Lina")
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if claimed.ID != linaMember {
		t.Errorf("member id = %v, want the existing %v — her chores would be orphaned",
			claimed.ID, linaMember)
	}
}

// A code must not be mintable against a member who already has an account, or a
// leaked code would hand over somebody else's identity.
func TestCannotMintAClaimForAnOccupiedMember(t *testing.T) {
	s := newTestStore(t)

	owner := account(t, s, "camille")
	id, admin := household(t, s, owner, "Maison")

	_, digest, err := auth.NewInviteCode()
	if err != nil {
		t.Fatalf("NewInviteCode: %v", err)
	}

	_, err = s.CreateInvite(t.Context(), id, store.NewInvite{
		CodeHash:    digest,
		Role:        store.RoleAdult,
		ClaimMember: &admin.ID, // the admin already has an account
		CreatedBy:   admin.ID,
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if !errors.Is(err, store.ErrAlreadyMember) {
		t.Errorf("err = %v, want ErrAlreadyMember", err)
	}
}
