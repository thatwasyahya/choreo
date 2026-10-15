package store_test

import (
	"errors"
	"testing"

	"github.com/thatwasyahya/choreo/server/internal/store"
)

func TestLoginIsUnique(t *testing.T) {
	s := newTestStore(t)
	account(t, s, "yahya")

	_, err := s.Identity.CreateAccount(t.Context(), "yahya", "$x", "$y")
	if !errors.Is(err, store.ErrLoginTaken) {
		t.Errorf("err = %v, want ErrLoginTaken", err)
	}
}

// citext: the same login in another case is the same login.
func TestLoginIsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	account(t, s, "Yahya")

	if _, err := s.Identity.CreateAccount(t.Context(), "yahya", "$x", "$y"); !errors.Is(err, store.ErrLoginTaken) {
		t.Errorf("err = %v, want ErrLoginTaken for a different case", err)
	}

	found, err := s.Identity.AccountByLogin(t.Context(), "YAHYA")
	if err != nil {
		t.Fatalf("AccountByLogin: %v", err)
	}
	if found.Login != "Yahya" {
		t.Errorf("Login = %q — the capital the user chose should be preserved", found.Login)
	}
}

func TestUnknownLoginIsDistinguishable(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Identity.AccountByLogin(t.Context(), "personne")
	if !errors.Is(err, store.ErrAccountUnknown) {
		t.Errorf("err = %v, want ErrAccountUnknown", err)
	}
}

// Probing someone else's device id must give the same answer as probing one
// that does not exist.
func TestDeviceOfAnotherAccountIsSimplyUnknown(t *testing.T) {
	s := newTestStore(t)

	mine := account(t, s, "camille")
	theirs := account(t, s, "jordan")
	theirDevice := device(t, s, theirs)

	if err := s.Identity.CheckDevice(t.Context(), theirDevice, mine); !errors.Is(err, store.ErrDeviceUnknown) {
		t.Errorf("err = %v, want ErrDeviceUnknown", err)
	}
}

// Revocation has to bite on the next request, not when the token expires.
func TestRevokingADeviceInvalidatesItsAccessImmediately(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	access, _ := session(t, s, acc, dev)

	if _, err := s.Identity.AuthenticateAccess(t.Context(), access); err != nil {
		t.Fatalf("a fresh access token was rejected: %v", err)
	}

	if err := s.Identity.RevokeDevice(t.Context(), acc, dev); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	if _, err := s.Identity.AuthenticateAccess(t.Context(), access); !errors.Is(err, store.ErrSessionUnknown) {
		t.Errorf("err = %v, want ErrSessionUnknown after revocation", err)
	}
}

func TestRecoveryRevokesOtherDevicesOnly(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	kept := device(t, s, acc)
	lost := device(t, s, acc)

	keptAccess, _ := session(t, s, acc, kept)
	lostAccess, _ := session(t, s, acc, lost)

	if err := s.Identity.RevokeOtherDevices(t.Context(), acc, kept); err != nil {
		t.Fatalf("RevokeOtherDevices: %v", err)
	}

	if _, err := s.Identity.AuthenticateAccess(t.Context(), keptAccess); err != nil {
		t.Errorf("the device we signed in from was revoked too: %v", err)
	}
	if _, err := s.Identity.AuthenticateAccess(t.Context(), lostAccess); !errors.Is(err, store.ErrSessionUnknown) {
		t.Errorf("the lost device still works: err = %v", err)
	}
}

// --- refresh rotation -------------------------------------------------------

func TestRotationConsumesTheOldToken(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	oldAccess, refresh := session(t, s, acc, dev)

	newAccess, _ := rotate(t, s, refresh)

	if _, err := s.Identity.AuthenticateAccess(t.Context(), newAccess); err != nil {
		t.Errorf("the new access token does not work: %v", err)
	}
	if _, err := s.Identity.AuthenticateAccess(t.Context(), oldAccess); !errors.Is(err, store.ErrSessionUnknown) {
		t.Errorf("the superseded access token still works: err = %v", err)
	}
}

// The reason the grace window exists: the server rotated, the REPLY was lost,
// and the phone retries with the token it still holds. Treating that as theft
// locks an honest household out of its own data.
func TestReplayWithinGraceIsForgiven(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	_, refresh := session(t, s, acc, dev)

	rotate(t, s, refresh) // the successor exists but was never used

	if _, err := s.Identity.RotateRefresh(t.Context(), refresh); err != nil {
		t.Errorf("a lost-response retry was refused: %v", err)
	}
}

// Theft detection must survive the grace window: once a successor has actually
// been used, replaying its parent means two parties are alternating.
func TestReplayAfterSuccessorUsedIsRefused(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	_, first := session(t, s, acc, dev)

	_, second := rotate(t, s, first)
	rotate(t, s, second) // the successor has now been used

	if _, err := s.Identity.RotateRefresh(t.Context(), first); !errors.Is(err, store.ErrRefreshReused) {
		t.Errorf("err = %v, want ErrRefreshReused", err)
	}
}

// And it must survive the passage of time: a replay long after the rotation is
// not a lost packet.
func TestReplayAfterGraceExpiresIsRefused(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	_, refresh := session(t, s, acc, dev)

	rotate(t, s, refresh)

	// Age the rotation past the window rather than waiting for it.
	execSQL(t, `UPDATE sessions SET consumed_at = now() - interval '5 minutes'
	             WHERE refresh_hash = $1`, refresh)

	if _, err := s.Identity.RotateRefresh(t.Context(), refresh); !errors.Is(err, store.ErrRefreshReused) {
		t.Errorf("err = %v, want ErrRefreshReused once the grace window has passed", err)
	}
}

func TestRotationRefusesAnExpiredToken(t *testing.T) {
	s := newTestStore(t)

	acc := account(t, s, "yahya")
	dev := device(t, s, acc)
	_, refresh := session(t, s, acc, dev)

	execSQL(t, `UPDATE sessions SET refresh_expires_at = now() - interval '1 day'
	             WHERE refresh_hash = $1`, refresh)

	if _, err := s.Identity.RotateRefresh(t.Context(), refresh); !errors.Is(err, store.ErrSessionUnknown) {
		t.Errorf("err = %v, want ErrSessionUnknown", err)
	}
}

func TestRotationRefusesAnUnknownToken(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Identity.RotateRefresh(t.Context(), []byte("jamais emis")); !errors.Is(err, store.ErrSessionUnknown) {
		t.Errorf("err = %v, want ErrSessionUnknown", err)
	}
}
