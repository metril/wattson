package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/metril/wattson/internal/store"
)

// Scope is a coarse permission. admin satisfies every scope.
type Scope string

const (
	ScopeRead    Scope = "read"
	ScopeControl Scope = "control"
	ScopeAdmin   Scope = "admin"
)

var scopeOrder = []Scope{ScopeRead, ScopeControl, ScopeAdmin}

const (
	// SessionCookie names the session cookie.
	SessionCookie = "wattson_session"
	// WSProtocol is the WebSocket subprotocol; a token rides as a second
	// subprotocol "bearer.<token>".
	WSProtocol = "wattson.v1"
)

// ParseScopes validates in: non-empty, known scopes only. The result is
// deduplicated and ordered read < control < admin.
func ParseScopes(in []string) ([]Scope, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("at least one scope is required")
	}
	var out []Scope
	for _, want := range scopeOrder {
		if slices.Contains(in, string(want)) {
			out = append(out, want)
		}
	}
	for _, s := range in {
		if !slices.Contains(scopeOrder, Scope(s)) {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
	}
	return out, nil
}

// Identity is an authenticated caller.
type Identity struct {
	User        store.User
	Scopes      []Scope
	Via         string // "session" or "token"
	TokenID     int64  // set when Via == "token"
	SessionHash string // set when Via == "session"
}

// Has reports whether the identity holds s; admin satisfies every scope.
func (id Identity) Has(s Scope) bool {
	return slices.Contains(id.Scopes, s) || slices.Contains(id.Scopes, ScopeAdmin)
}

type ctxKey struct{}

// NewContext returns ctx carrying id.
func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the identity attached by Middleware, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
