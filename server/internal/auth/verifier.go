package auth

import (
	"context"
	"fmt"
)

// DefaultHashConcurrency is how many argon2id computations may run at once.
//
// Four, on a 2 vCPU box: enough that real logins never queue noticeably, low
// enough that the memory they hold is bounded.
const DefaultHashConcurrency = 4

// Verifier serialises password hashing behind a global ceiling.
//
// Without it the cost ceiling of decision D-005 is a fiction. Each argon2id
// computation holds 19 MiB, and the rate limiter is keyed per login and per
// address — so an attempt spread across many distinct logins passes both
// buckets and still arrives all at once. Four hundred simultaneous attempts
// would ask for nearly 8 GB on a machine that has 4.
//
// This is the piece that makes "it stays free" true rather than hopeful:
// under flood the server queues and slows down, it does not die.
type Verifier struct {
	sem chan struct{}
}

// NewVerifier bounds concurrent hashing to the given number.
func NewVerifier(concurrency int) *Verifier {
	if concurrency < 1 {
		concurrency = DefaultHashConcurrency
	}
	return &Verifier{sem: make(chan struct{}, concurrency)}
}

// Hash computes a password hash, waiting for a slot.
func (v *Verifier) Hash(ctx context.Context, password string) (string, error) {
	release, err := v.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	return HashPassword(password)
}

// Verify checks a password against a stored hash, waiting for a slot.
func (v *Verifier) Verify(ctx context.Context, hash, password string) (ok, needsRehash bool, err error) {
	release, err := v.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()

	return VerifyPassword(hash, password)
}

// SpendBudget burns the same work as Verify, for the case where the login does
// not exist.
//
// It takes a slot like any other computation: exempting it would hand an
// attacker an unbounded path to the expensive operation by simply sending
// logins that do not exist.
func (v *Verifier) SpendBudget(ctx context.Context) {
	release, err := v.acquire(ctx)
	if err != nil {
		// The caller is going away anyway; there is nothing left to equalise.
		return
	}
	defer release()

	SpendVerifyBudget()
}

func (v *Verifier) acquire(ctx context.Context) (func(), error) {
	select {
	case v.sem <- struct{}{}:
		return func() { <-v.sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: waiting for a hashing slot: %w", ctx.Err())
	}
}
