package auth

import (
	"sync"
	"time"
)

// Limiter counts failures per key inside a sliding window, plus attempts
// currently in flight. It is a cheap, non-blocking in-memory check: a login
// handler must call Allow before any password verification so that floods
// are rejected before they can queue on the argon2 semaphore.
//
// Allow reserves a slot, so N concurrent attempts on one key cannot all slip
// through before the first failure is recorded: recorded failures plus
// in-flight reservations never exceed max. Memory is bounded: at most max
// timestamps per key, at most max reservations per key, and expired keys are
// swept at least once per window.
type Limiter struct {
	max    int
	window time.Duration

	mu       sync.Mutex
	fails    map[string][]time.Time // oldest first, len <= max
	inflight map[string]int
	swept    time.Time
}

// NewLimiter allows maxFails failures per key within window (maxFails is at
// least 1).
func NewLimiter(maxFails int, window time.Duration) *Limiter {
	return &Limiter{max: max(maxFails, 1), window: window, fails: map[string][]time.Time{},
		inflight: map[string]int{}, swept: time.Now()}
}

// Reservation is one admitted attempt. Exactly one of Fail, Success or
// Release should be called; later calls are no-ops.
type Reservation struct {
	l    *Limiter
	key  string
	done bool // guarded by l.mu
}

// prune drops failures that have left the window and returns the rest.
func (l *Limiter) prune(key string, now time.Time) []time.Time {
	f := l.fails[key]
	i := 0
	for i < len(f) && now.Sub(f[i]) >= l.window {
		i++
	}
	if i == len(f) {
		delete(l.fails, key)
		return nil
	}
	if i > 0 {
		f = f[i:]
		l.fails[key] = f
	}
	return f
}

// inflightRetry is the retryAfter reported when only in-flight attempts (not
// recorded failures) are holding the key's slots.
const inflightRetry = time.Second

// Allow admits an attempt for key if recorded failures plus in-flight
// attempts are below the maximum, returning a Reservation. When refused, res
// is nil and retryAfter is the time until the oldest counted failure leaves
// the window (or a short fixed delay if only in-flight attempts block it).
func (l *Limiter) Allow(key string) (res *Reservation, retryAfter time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if len(f)+l.inflight[key] < l.max {
		l.inflight[key]++
		return &Reservation{l: l, key: key}, 0
	}
	if len(f) >= l.max {
		return nil, f[0].Add(l.window).Sub(now)
	}
	return nil, inflightRetry
}

// release gives the slot back; callers hold l.mu.
func (r *Reservation) release() bool {
	if r.done {
		return false
	}
	r.done = true
	if n := r.l.inflight[r.key] - 1; n > 0 {
		r.l.inflight[r.key] = n
	} else {
		delete(r.l.inflight, r.key)
	}
	return true
}

// Fail converts the reservation into a recorded failure.
func (r *Reservation) Fail() {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	if r.release() {
		r.l.recordFail(r.key, time.Now())
	}
}

// Success releases the slot and forgets the key's recorded failures.
func (r *Reservation) Success() {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	if r.release() {
		delete(r.l.fails, r.key)
	}
}

// Release gives the slot back without recording anything (for attempts that
// ended without a credential verdict, such as a cancelled request).
func (r *Reservation) Release() {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	r.release()
}

func (l *Limiter) recordFail(key string, now time.Time) {
	if now.Sub(l.swept) >= l.window {
		for k := range l.fails {
			l.prune(k, now)
		}
		l.swept = now
	}
	f := append(l.prune(key, now), now)
	if len(f) > l.max {
		f = f[len(f)-l.max:]
	}
	l.fails[key] = f
}

// Fail records a failure for key without a reservation.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordFail(key, time.Now())
}

// Reset forgets key's recorded failures. In-flight reservations stay.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
