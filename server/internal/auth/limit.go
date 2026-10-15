package auth

import (
	"sync"
	"time"
)

// Limiter is an in-process token bucket, keyed by whatever the caller chooses —
// the login for a sign-in attempt, the address for everything else.
//
// No Redis: one more moving part would have to justify its RAM (operating rule
// 4), and there is one process. The consequence is written down rather than
// discovered: **these limits reset on restart and do not span a second
// instance.** That is acceptable on one VPS and is the thing to revisit the day
// there are two, not before.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	burst  float64
	refill float64 // tokens per second
	now    func() time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

// sweepAbove bounds memory: once there are more keys than this, idle buckets are
// dropped. An attacker rotating keys cannot grow the map without bound.
const sweepAbove = 10_000

// NewLimiter allows burst events, refilling to full over per.
func NewLimiter(burst int, per time.Duration) *Limiter {
	if burst < 1 {
		burst = 1
	}
	if per <= 0 {
		per = time.Second
	}

	return &Limiter{
		buckets: make(map[string]*bucket),
		burst:   float64(burst),
		refill:  float64(burst) / per.Seconds(),
		now:     time.Now,
	}
}

// Allow consumes a token for key and reports whether it was available.
func (l *Limiter) Allow(key string) bool {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= sweepAbove {
			l.sweep(now)
		}
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	}

	// Refill for the time that passed, capped at burst.
	elapsed := now.Sub(b.seen).Seconds()
	if elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.refill)
	}
	b.seen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have had time to refill completely: a full bucket is
// indistinguishable from one that never existed.
func (l *Limiter) sweep(now time.Time) {
	full := time.Duration(l.burst/l.refill) * time.Second

	for key, b := range l.buckets {
		if now.Sub(b.seen) >= full {
			delete(l.buckets, key)
		}
	}
}
