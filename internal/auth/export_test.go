package auth

import "context"

// Size reports how many keys the limiter tracks.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.fails) + len(l.inflight)
}

// TouchedLen reports how many tokens the authenticator remembers touching.
func (a *Authenticator) TouchedLen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.touched)
}

// QueuedHook returns a ctx that calls f when derive is about to wait on the
// hashing semaphore.
func QueuedHook(ctx context.Context, f func()) context.Context {
	return context.WithValue(ctx, queuedKey{}, f)
}
