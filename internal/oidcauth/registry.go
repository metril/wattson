package oidcauth

import (
	"context"
	"net/http"
	"sync"
)

// Registry caches discovered Providers per slug, so a login does not repeat
// discovery and JWKS fetches. An entry is reused only while the caller's
// version string (for example the provider row's updated_at) is unchanged.
type Registry struct {
	client *http.Client

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	version string
	p       *Provider
}

// NewRegistry returns an empty Registry whose providers use client for
// every HTTP call (nil means http.DefaultClient).
func NewRegistry(client *http.Client) *Registry {
	return &Registry{client: client, cache: map[string]cached{}}
}

// Get returns the Provider for cfg.Slug, discovering it when none is cached
// for that version. Failures are not cached.
func (r *Registry) Get(ctx context.Context, cfg Config, version string) (*Provider, error) {
	r.mu.Lock()
	c, ok := r.cache[cfg.Slug]
	r.mu.Unlock()
	if ok && c.version == version {
		return c.p, nil
	}
	p, err := New(ctx, cfg, r.client)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[cfg.Slug] = cached{version: version, p: p}
	r.mu.Unlock()
	return p, nil
}

// Forget drops the cached Provider of slug (for example after deletion).
func (r *Registry) Forget(slug string) {
	r.mu.Lock()
	delete(r.cache, slug)
	r.mu.Unlock()
}
