package oidcauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/metril/wattson/internal/oidcauth/oidctest"
)

const redirectURI = "https://app.example/api/auth/oidc/authentik/callback"

func newProvider(t *testing.T, idp *oidctest.IDP, cfg Config) *Provider {
	t.Helper()
	cfg.Issuer = idp.Issuer
	cfg.ClientID = idp.ClientID
	cfg.ClientSecret = idp.ClientSecret
	p, err := New(context.Background(), cfg, idp.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// login drives a full authorize round trip with the given claims and
// returns the code to redeem.
func login(t *testing.T, idp *oidctest.IDP, p *Provider, claims map[string]any, nonce, verifier string) string {
	t.Helper()
	idp.Next(claims)
	code, state := idp.Authorize(t, p.AuthCodeURL("st-1", nonce, verifier, redirectURI))
	if code == "" || state != "st-1" {
		t.Fatalf("authorize: code %q state %q", code, state)
	}
	return code
}

func TestDiscover(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	if _, err := New(context.Background(), Config{Issuer: idp.Issuer, ClientID: "c"}, idp.Client()); err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := New(context.Background(), Config{Issuer: idp.URL + "/wrong", ClientID: "c"}, idp.Client()); err == nil {
		t.Fatal("New with a bad issuer succeeded")
	}
}

func TestAuthCodeURLHasPKCEChallenge(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups", Scopes: []string{"groups"}})

	u := p.AuthCodeURL("state-1", "nonce-1", "verifier-1", redirectURI)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("AuthCodeURL = %s, missing PKCE challenge", u)
	}
	if q.Get("nonce") != "nonce-1" || q.Get("state") != "state-1" || q.Get("redirect_uri") != redirectURI {
		t.Fatalf("AuthCodeURL = %s", u)
	}
	if s := strings.Fields(q.Get("scope")); len(s) != 4 || s[0] != "openid" || s[3] != "groups" {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
}

func TestExchangeGroupsFromList(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	code := login(t, idp, p, map[string]any{
		"sub": "user-1", "email": "user@example.com", "email_verified": true, "name": "User One",
		"preferred_username": "user1", "groups": []any{"admins", "users"},
	}, "n", "verifier-1")

	cl, err := p.Exchange(context.Background(), code, "verifier-1", "n", redirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if cl.Issuer != idp.Issuer || cl.Subject != "user-1" || cl.Email != "user@example.com" || !cl.EmailVerified ||
		cl.Name != "User One" || cl.PreferredUsername != "user1" {
		t.Fatalf("claims = %+v", cl)
	}
	if len(cl.Groups) != 2 || cl.Groups[0] != "admins" || cl.Groups[1] != "users" {
		t.Fatalf("groups = %v", cl.Groups)
	}
}

func TestExchangeGroupsFromString(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	code := login(t, idp, p, map[string]any{"sub": "u", "groups": "admins, users"}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if len(cl.Groups) != 2 || cl.Groups[0] != "admins" || cl.Groups[1] != "users" {
		t.Fatalf("groups = %v", cl.Groups)
	}
}

func TestExchangeMissingGroupsClaim(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	// No userinfo on the fake: the fallback 404s, which must not fail the
	// login, only be reported on the claims.
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if len(cl.Groups) != 0 || cl.UserInfoErr == nil {
		t.Fatalf("groups %v, UserInfoErr %v", cl.Groups, cl.UserInfoErr)
	}
	if n := idp.Requests("/userinfo"); n != 1 {
		t.Fatalf("userinfo requests = %d, want 1", n)
	}
}

func TestExchangeGroupsFromUserInfo(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	// Authentik without "include claims in ID token": groups only at userinfo.
	idp.SetUserInfo(map[string]any{"sub": "u", "email": "userinfo@example.com", "name": "User Four",
		"preferred_username": "user4", "groups": []any{"localadmin"}})
	code := login(t, idp, p, map[string]any{"sub": "u", "email": "idtoken@example.com", "email_verified": true}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if cl.UserInfoErr != nil || len(cl.Groups) != 1 || cl.Groups[0] != "localadmin" {
		t.Fatalf("groups %v, err %v", cl.Groups, cl.UserInfoErr)
	}
	if cl.Email != "idtoken@example.com" || !cl.EmailVerified {
		t.Fatalf("email %q verified %v: the ID token must win", cl.Email, cl.EmailVerified)
	}
	if cl.Name != "User Four" || cl.PreferredUsername != "user4" {
		t.Fatalf("claims = %+v", cl)
	}
}

func TestUserInfoEmailVerifiedFollowsEmailSource(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	// The ID token has no email; userinfo supplies both email and its flag.
	idp.SetUserInfo(map[string]any{"sub": "u", "email": "ui@example.com", "email_verified": true, "groups": []any{}})
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil || cl.Email != "ui@example.com" || !cl.EmailVerified {
		t.Fatalf("claims %+v, err %v", cl, err)
	}
	// The ID token carries an unverified email; userinfo's flag for a
	// different address must not upgrade it.
	idp.SetUserInfo(map[string]any{"sub": "u", "email": "other@example.com", "email_verified": true})
	code = login(t, idp, p, map[string]any{"sub": "u", "email": "idt@example.com"}, "n", "v")
	cl, err = p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil || cl.Email != "idt@example.com" || cl.EmailVerified {
		t.Fatalf("claims %+v, err %v", cl, err)
	}
}

func TestExchangeSkipsUserInfoWhenIDTokenHasGroups(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	idp.SetUserInfo(map[string]any{"sub": "u", "groups": []any{"localadmin"}})
	code := login(t, idp, p, map[string]any{"sub": "u", "groups": []any{}}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if len(cl.Groups) != 0 || idp.Requests("/userinfo") != 0 {
		t.Fatalf("groups %v, userinfo requests %d", cl.Groups, idp.Requests("/userinfo"))
	}
}

func TestExchangeUserInfoSubjectMismatch(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{GroupsClaim: "groups"})
	idp.SetUserInfo(map[string]any{"sub": "someone-else", "groups": []any{"localadmin"}})
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	if _, err := p.Exchange(context.Background(), code, "v", "n", redirectURI); err == nil {
		t.Fatal("Exchange accepted a userinfo document for a different subject")
	}
}

func TestNonceMismatch(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{})
	idp.ForceNonce("other")
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	if _, err := p.Exchange(context.Background(), code, "v", "n", redirectURI); !errors.Is(err, ErrNonce) {
		t.Fatalf("err = %v, want ErrNonce", err)
	}
	// An ID token with no nonce at all is refused as well.
	idp.ForceNonce("")
	code = login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	if _, err := p.Exchange(context.Background(), code, "v", "n", redirectURI); !errors.Is(err, ErrNonce) {
		t.Fatalf("missing nonce: err = %v, want ErrNonce", err)
	}
}

func TestPKCEEnforced(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{})
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "right-verifier")
	if _, err := p.Exchange(context.Background(), code, "wrong-verifier", "n", redirectURI); err == nil {
		t.Fatal("Exchange with the wrong verifier succeeded")
	}
}

func TestEmailVerified(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{})
	for name, tc := range map[string]struct {
		claims map[string]any
		want   bool
	}{
		"true":    {map[string]any{"sub": "u", "email": "a@x.test", "email_verified": true}, true},
		"false":   {map[string]any{"sub": "u", "email": "a@x.test", "email_verified": false}, false},
		"missing": {map[string]any{"sub": "u", "email": "a@x.test"}, false},
		"string":  {map[string]any{"sub": "u", "email": "a@x.test", "email_verified": "true"}, true},
	} {
		code := login(t, idp, p, tc.claims, "n", "v")
		cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cl.EmailVerified != tc.want {
			t.Errorf("%s: EmailVerified = %v, want %v", name, cl.EmailVerified, tc.want)
		}
	}
}

func TestExchangeUnknownCode(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{})
	if _, err := p.Exchange(context.Background(), "no-such-code", "v", "n", redirectURI); err == nil {
		t.Fatal("Exchange of an unknown code succeeded")
	}
}

func TestExchangeCodeSingleUse(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	p := newProvider(t, idp, Config{})
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	if _, err := p.Exchange(context.Background(), code, "v", "n", redirectURI); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exchange(context.Background(), code, "v", "n", redirectURI); err == nil {
		t.Fatal("a code was redeemed twice")
	}
}

func TestAuthorizeAndAdminDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cfg        Config
		cl         Claims
		forbid     bool
		admin, mgd bool
	}{
		{"no rules", Config{}, Claims{Groups: []string{"x"}}, false, false, false},
		{"admin group matched", Config{AdminGroup: "admins"}, Claims{Groups: []string{"admins", "x"}}, false, true, true},
		{"admin group missing", Config{AdminGroup: "admins"}, Claims{Groups: []string{"x"}}, false, false, true},
		{"admin group case-insensitive", Config{AdminGroup: "LocalAdmin"}, Claims{Groups: []string{"localadmin"}}, false, true, true},
		{"allowed groups matched", Config{AllowedGroups: []string{"users"}}, Claims{Groups: []string{"users"}}, false, false, false},
		{"allowed groups not matched", Config{AllowedGroups: []string{"users"}}, Claims{Groups: []string{"other"}}, true, false, false},
		{"allowed groups, none sent", Config{AllowedGroups: []string{"users"}}, Claims{}, true, false, false},
		{"allowed emails matched", Config{AllowedEmails: []string{"A@Example.com"}}, Claims{Email: "a@example.com", EmailVerified: true}, false, false, false},
		{"allowed emails unverified", Config{AllowedEmails: []string{"a@example.com"}}, Claims{Email: "a@example.com"}, true, false, false},
		{"allowed emails not matched", Config{AllowedEmails: []string{"a@example.com"}}, Claims{Email: "b@example.com", EmailVerified: true}, true, false, false},
		{"unverified email, group rule only", Config{AllowedGroups: []string{"g"}}, Claims{Email: "a@x.test", Groups: []string{"g"}}, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Authorize(tc.cl)
			if tc.forbid != errors.Is(err, ErrForbidden) || (!tc.forbid && err != nil) {
				t.Fatalf("Authorize = %v, forbid %v", err, tc.forbid)
			}
			admin, managed := tc.cfg.AdminDecision(tc.cl)
			if admin != tc.admin || managed != tc.mgd {
				t.Fatalf("AdminDecision = %v, %v; want %v, %v", admin, managed, tc.admin, tc.mgd)
			}
		})
	}
}

func TestResolveIssuerTrailingSlash(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t, oidctest.WithIssuerPath("/application/o/wattson/"))
	want := idp.URL + "/application/o/wattson/"
	if idp.Issuer != want {
		t.Fatalf("fake issuer %q", idp.Issuer)
	}
	for _, in := range []string{
		idp.URL + "/application/o/wattson/",
		idp.URL + "/application/o/wattson",
		idp.URL + "/application/o/wattson/.well-known/openid-configuration",
	} {
		got, err := ResolveIssuer(context.Background(), idp.Client(), in)
		if err != nil || got != want {
			t.Errorf("ResolveIssuer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	p, err := New(context.Background(), Config{Issuer: want, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret}, idp.Client())
	if err != nil || p == nil {
		t.Fatalf("New with the Authentik-style issuer: %v", err)
	}
	// A login through it works end to end.
	code := login(t, idp, p, map[string]any{"sub": "u"}, "n", "v")
	cl, err := p.Exchange(context.Background(), code, "v", "n", redirectURI)
	if err != nil || cl.Issuer != want {
		t.Fatalf("claims %+v, err %v", cl, err)
	}
}

func TestResolveIssuerErrors(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	ctx := context.Background()
	for _, in := range []string{"", "not a url", "ftp://x/y", "http://127.0.0.1:1", idp.URL + "/missing"} {
		if got, err := ResolveIssuer(ctx, idp.Client(), in); err == nil {
			t.Errorf("ResolveIssuer(%q) = %q, want an error", in, got)
		}
	}
	if got, err := ResolveIssuer(ctx, nil, idp.URL); err != nil || got != idp.Issuer {
		t.Errorf("nil client: %q, %v", got, err)
	}
}

func TestRegistryCaches(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	ctx := context.Background()
	r := NewRegistry(idp.Client())
	cfg := Config{Slug: "a", Issuer: idp.Issuer, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret}
	wk := "/.well-known/openid-configuration"

	p1, err := r.Get(ctx, cfg, "v1")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := r.Get(ctx, cfg, "v1")
	if err != nil || p2 != p1 || idp.Requests(wk) != 1 {
		t.Fatalf("same version: same=%v err=%v discoveries=%d", p2 == p1, err, idp.Requests(wk))
	}
	cfg.ClientID = "changed"
	p3, err := r.Get(ctx, cfg, "v2")
	if err != nil || p3 == p1 || idp.Requests(wk) != 2 {
		t.Fatalf("new version: err=%v discoveries=%d", err, idp.Requests(wk))
	}
	if p3.Config().ClientID != "changed" {
		t.Fatalf("client id = %q", p3.Config().ClientID)
	}
	// Another slug is cached independently.
	other := cfg
	other.Slug = "b"
	if _, err := r.Get(ctx, other, "v1"); err != nil || idp.Requests(wk) != 3 {
		t.Fatalf("other slug: err=%v discoveries=%d", err, idp.Requests(wk))
	}
	r.Forget("b")
	if _, err := r.Get(ctx, other, "v1"); err != nil || idp.Requests(wk) != 4 {
		t.Fatalf("after Forget: err=%v discoveries=%d", err, idp.Requests(wk))
	}
}

func TestRegistryDoesNotCacheFailures(t *testing.T) {
	t.Parallel()
	idp := oidctest.NewIDP(t)
	var hits atomic.Int32
	r := NewRegistry(idp.Client())
	bad := Config{Slug: "a", Issuer: idp.URL + "/nope", ClientID: "c"}
	for range 2 {
		hits.Add(1)
		if _, err := r.Get(context.Background(), bad, "v1"); err == nil {
			t.Fatal("discovery of a bad issuer succeeded")
		}
	}
	if n := idp.Requests("/nope/.well-known/openid-configuration"); n != int(hits.Load()) {
		t.Fatalf("failed discoveries = %d, want %d (not cached)", n, hits.Load())
	}
	good := bad
	good.Issuer = idp.Issuer
	if _, err := r.Get(context.Background(), good, "v1"); err != nil {
		t.Fatalf("recovery after failure: %v", err)
	}
}
