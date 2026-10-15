package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVerifierRoundTrip(t *testing.T) {
	v := NewVerifier(2)
	ctx := t.Context()

	hash, err := v.Hash(ctx, "un mot de passe correct")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	ok, rehash, err := v.Verify(ctx, hash, "un mot de passe correct")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("the correct password was rejected")
	}
	if rehash {
		t.Error("a fresh hash should not ask to be rehashed")
	}
}

// The ceiling is what keeps a login flood from asking for more memory than the
// machine has. If it stops being enforced, the cost promise of D-005 quietly
// becomes untrue, so it gets a deterministic test rather than a comment.
func TestVerifierCeilingIsEnforced(t *testing.T) {
	v := NewVerifier(1)

	release, err := v.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Nothing will free the only slot, so this deadline always expires: the
	// test is bounded, not racy.
	blocked, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, _, err := v.Verify(blocked, "irrelevant", "irrelevant"); err == nil {
		t.Error("a second computation got through a ceiling of one")
	}

	release()

	// With the slot back, the call proceeds and fails for its own reason.
	if _, _, err := v.Verify(t.Context(), "not-a-hash", "x"); !errors.Is(err, ErrHashMalformed) {
		t.Errorf("after release, err = %v, want ErrHashMalformed", err)
	}
}

// Spending the budget for an unknown login must take a slot too. Exempting it
// would hand an attacker an unbounded path to the expensive operation by simply
// sending logins that do not exist.
func TestSpendBudgetTakesASlot(t *testing.T) {
	v := NewVerifier(1)

	release, err := v.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	done := make(chan struct{})
	blocked, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	go func() {
		v.SpendBudget(blocked)
		close(done)
	}()

	select {
	case <-done:
		// It gave up on the context rather than computing: correct.
	case <-time.After(2 * time.Second):
		t.Error("SpendBudget ignored the ceiling and never returned")
	}
}

func TestNewVerifierRejectsNonsenseConcurrency(t *testing.T) {
	for _, concurrency := range []int{0, -1} {
		v := NewVerifier(concurrency)
		if got := cap(v.sem); got != DefaultHashConcurrency {
			t.Errorf("NewVerifier(%d) capacity = %d, want the default %d",
				concurrency, got, DefaultHashConcurrency)
		}
	}
}
