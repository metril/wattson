package auth

import (
	"sync"
	"time"
)

// Limiter counts failures per key inside a sliding window. It is a cheap,
// non-blocking in-memory check: a login handler must call Allow before any
// password verification so that floods are rejected before they can queue on
// the argon2 semaphore. Memory is bounded: at most max timestamps per key,
// and expired keys are swept at least once per window.
type Limiter struct {
	max    int
	window time.Duration

	mu    sync.Mutex
	fails map[string][]time.Time // oldest first, len <= max
	swept time.Time
}

// NewLimiter allows maxFails failures per key within window (max is at least 1).
func NewLimiter(maxFails int, window time.Duration) *Limiter {
	return &Limiter{max: max(maxFails, 1), window: window, fails: map[string][]time.Time{}, swept: time.Now()}
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

// Allow reports whether key may attempt. When it may not, retryAfter is the
// time until the oldest counted failure leaves the window.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if len(f) < l.max {
		return true, 0
	}
	return false, f[0].Add(l.window).Sub(now)
}

// Fail records a failed attempt for key.
func (l *Limiter) Fail(key string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
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

// Reset forgets key's failures (call after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
