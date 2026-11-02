package auth

// Size reports how many keys the limiter tracks.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.fails)
}
