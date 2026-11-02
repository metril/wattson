package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/metril/wattson/internal/store"
)

const (
	touchEvery  = 60 * time.Second
	wsBearerPfx = "bearer."
	tokenLen    = len(tokenPrefix) + 43
)

// Authenticator resolves requests to identities and enforces the CSRF rule.
type Authenticator struct {
	st        *store.Store
	publicURL func(context.Context) string
	log       *slog.Logger

	mu      sync.Mutex
	touched map[int64]time.Time // token id -> last last_used_at write
	tswept  time.Time
}

// NewAuthenticator builds an Authenticator. publicURL returns the configured
// external URL ("" if unset), consulted for the CSRF Origin check.
func NewAuthenticator(st *store.Store, publicURL func(context.Context) string, logger *slog.Logger) *Authenticator {
	return &Authenticator{st: st, publicURL: publicURL, log: logger, touched: map[int64]time.Time{}}
}

// Identify resolves the caller. The first credential present wins, in the
// order Authorization: Bearer, Sec-WebSocket-Protocol "bearer.<tok>", session
// cookie; if it is invalid the caller is anonymous (no fallback).
func (a *Authenticator) Identify(r *http.Request) (Identity, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if scheme, tok, ok := strings.Cut(h, " "); ok && strings.EqualFold(scheme, "Bearer") {
			return a.identifyToken(r, strings.TrimSpace(tok))
		}
	}
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if tok, ok := strings.CutPrefix(strings.TrimSpace(p), wsBearerPfx); ok {
				return a.identifyToken(r, tok)
			}
		}
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return a.identifySession(r, c.Value)
	}
	return Identity{}, false
}

func (a *Authenticator) identifySession(r *http.Request, cookie string) (Identity, bool) {
	hash := HashSecret(cookie)
	u, err := a.st.SessionUser(r.Context(), hash, time.Now())
	if err != nil {
		a.logLookup("session", err)
		return Identity{}, false
	}
	scopes := []Scope{ScopeRead, ScopeControl}
	if u.IsAdmin {
		scopes = append(scopes, ScopeAdmin)
	}
	return Identity{User: u, Scopes: scopes, Via: "session", SessionHash: hash}, true
}

func (a *Authenticator) identifyToken(r *http.Request, tok string) (Identity, bool) {
	if len(tok) != tokenLen || !strings.HasPrefix(tok, tokenPrefix) {
		return Identity{}, false // cheap reject before touching the DB
	}
	t, u, err := a.st.TokenUser(r.Context(), HashSecret(tok))
	if err != nil {
		a.logLookup("token", err)
		return Identity{}, false
	}
	scopes, err := ParseScopes(t.Scopes)
	if err != nil {
		a.log.Warn("auth: stored token scopes invalid", "token_id", t.ID, "err", err)
		return Identity{}, false
	}
	if !u.IsAdmin {
		scopes = dropAdmin(scopes)
		if len(scopes) == 0 {
			return Identity{}, false
		}
	}
	now := time.Now()
	if a.touchDue(t.ID, now) {
		if err := a.st.TouchAPIToken(r.Context(), t.ID, now); err != nil {
			a.log.Warn("auth: touch token failed", "token_id", t.ID, "err", err)
		}
	}
	return Identity{User: u, Scopes: scopes, Via: "token", TokenID: t.ID}, true
}

func (a *Authenticator) logLookup(kind string, err error) {
	if err != store.ErrNotFound {
		a.log.Warn("auth: credential lookup failed", "kind", kind, "err", err)
	}
}

func dropAdmin(in []Scope) []Scope {
	out := make([]Scope, 0, len(in))
	for _, s := range in {
		if s != ScopeAdmin {
			out = append(out, s)
		}
	}
	return out
}

// touchDue reports (and records) whether token id's last_used_at is due for
// a write: at most one per 60 s per token.
func (a *Authenticator) touchDue(id int64, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.touched[id]; ok && now.Sub(last) < touchEvery {
		return false
	}
	if now.Sub(a.tswept) >= touchEvery { // drop entries that no longer throttle
		for k, t := range a.touched {
			if now.Sub(t) >= touchEvery {
				delete(a.touched, k)
			}
		}
		a.tswept = now
	}
	a.touched[id] = now
	return true
}

// Middleware attaches the caller's Identity to the request context when
// credentials are valid; otherwise the request continues anonymously. A
// cookie-authenticated, non-safe-method request whose Origin header names a
// foreign host is refused with 403 "csrf". Bearer requests are exempt.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.Identify(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if id.Via == "session" && !safeMethod(r.Method) && !a.originOK(r) {
			writeCSRF(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), id)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// originOK is true when the Origin header names this request's Host or the
// public URL's host (default ports normalized per scheme). With no Origin,
// the request passes unless the browser says Sec-Fetch-Site: cross-site.
func (a *Authenticator) originOK(r *http.Request) bool {
	vals := r.Header.Values("Origin")
	if len(vals) == 0 {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	o, err := url.Parse(vals[0])
	if err != nil || o.Host == "" { // includes "null"
		return false
	}
	want := canonHost(o.Host, o.Scheme)
	if canonHost(r.Host, o.Scheme) == want {
		return true
	}
	if a.publicURL != nil {
		if p, err := url.Parse(a.publicURL(r.Context())); err == nil && p.Host != "" &&
			canonHost(p.Host, p.Scheme) == want {
			return true
		}
	}
	return false
}

// canonHost lowercases hostport and drops the port when it is scheme's
// default (443 for https, 80 for http).
func canonHost(hostport, scheme string) string {
	u, err := url.Parse("//" + hostport)
	if err != nil {
		return strings.ToLower(hostport)
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return host
}

func writeCSRF(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"code": "csrf", "message": "cross-origin request refused"}})
}
