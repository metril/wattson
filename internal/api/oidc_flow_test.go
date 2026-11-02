package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/oidcauth/oidctest"
	"github.com/metril/wattson/internal/store"
)

type flowEnv struct {
	ts    *testServer
	idp   *oidctest.IDP
	admin *http.Client
}

func newFlow(t *testing.T, provider map[string]any, idpOpts ...oidctest.Option) flowEnv {
	t.Helper()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idp := oidctest.NewIDP(t, idpOpts...)
	ts.addProvider(t, admin, idp, "authentik", provider)
	return flowEnv{ts: ts, idp: idp, admin: admin}
}

func (e flowEnv) login(t *testing.T, claims map[string]any) (*http.Client, *http.Response) {
	t.Helper()
	c := e.ts.client(t)
	return c, e.ts.oidcLogin(t, c, e.idp, "authentik", "", claims)
}

func (e flowEnv) me(t *testing.T, c *http.Client) (int, MeOut) {
	t.Helper()
	resp, b := e.ts.do(t, c, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		return resp.StatusCode, MeOut{}
	}
	return 200, decode[MeOut](t, b)
}

func hasSession(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == "wattson_session" && c.Value != "" && c.MaxAge >= 0 {
			return true
		}
	}
	return false
}

func wantRedirect(t *testing.T, resp *http.Response, loc string) {
	t.Helper()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != loc {
		t.Fatalf("response %d Location %q, want 302 %q", resp.StatusCode, resp.Header.Get("Location"), loc)
	}
}

func riderClaims() map[string]any {
	return map[string]any{"sub": "u1", "email": "a@x.test", "email_verified": true, "preferred_username": "alice",
		"groups": []any{"riders"}}
}

func TestOIDCLoginCreatesUser(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	c, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
	if !hasSession(resp) {
		t.Error("no session cookie on the callback response")
	}
	code, me := e.me(t, c)
	if code != 200 || me.User.Username != "alice" || me.User.IsAdmin || me.Via != "session" || !me.User.OIDC || me.User.Email != "a@x.test" {
		t.Fatalf("me = %d %+v", code, me)
	}
	// A second login finds the same user.
	c2, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
	if _, me2 := e.me(t, c2); me2.User.ID != me.User.ID {
		t.Errorf("second login user %d, want %d", me2.User.ID, me.User.ID)
	}
	// The state cookie is cleared at the callback.
	cleared := false
	for _, ck := range resp.Cookies() {
		if ck.Name == "wattson_oidc" && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("state cookie not cleared")
	}
}

func TestOIDCLoginCookieAttributes(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	resp, _ := e.ts.do(t, e.ts.client(t), "GET", "/api/auth/oidc/authentik/login?return_to=/x", nil, "X-Forwarded-Proto", "https")
	var ck *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "wattson_oidc" {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/api/auth/oidc/" || ck.MaxAge != 300 || !ck.Secure {
		t.Fatalf("state cookie = %+v", ck)
	}
	q := e.idp.LastAuthorize()
	if q != nil {
		t.Fatalf("IdP was contacted by the login redirect itself")
	}
	u, _ := url.Parse(resp.Header.Get("Location"))
	q = u.Query()
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if q.Get(k) == "" {
			t.Errorf("authorize URL lacks %s: %s", k, u)
		}
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != e.idp.ClientID ||
		q.Get("redirect_uri") != "https://"+strings.TrimPrefix(e.ts.URL, "http://")+"/api/auth/oidc/authentik/callback" ||
		q.Get("scope") != "openid profile email groups" {
		t.Errorf("authorize URL = %s", u)
	}
}

func TestOIDCReturnTo(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	for in, want := range map[string]string{
		"/settings?tab=bike":     "/settings?tab=bike",
		"//evil.example":         "/",
		"/\\evil.example":        "/",
		"https://evil.example":   "/",
		"https://evil.example/x": "/",
		"javascript:alert(1)":    "/",
		"relative/path":          "/",
		"/ok\r\nSet-Cookie: x=1": "/",
		"":                       "/",
	} {
		c := e.ts.client(t)
		resp := e.ts.oidcLogin(t, c, e.idp, "authentik", in, riderClaims())
		if resp.StatusCode != 302 || resp.Header.Get("Location") != want {
			t.Errorf("return_to %q: %d Location %q, want %q", in, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

func TestOIDCAllowedGroups(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"allowed_groups": []string{"riders"}})
	claims := riderClaims()
	claims["groups"] = []any{"other"}
	_, resp := e.login(t, claims)
	wantRedirect(t, resp, "/login?error=forbidden")
	if hasSession(resp) {
		t.Error("a forbidden login got a session cookie")
	}
	if n, _ := e.ts.Store.CountUsers(context.Background()); n != 1 {
		t.Errorf("users = %d, a forbidden login created one", n)
	}
	if !strings.Contains(e.ts.Log.String(), "reason=forbidden") || !strings.Contains(e.ts.Log.String(), "slug=authentik") {
		t.Errorf("failure not logged: %s", e.ts.Log.String())
	}
	_, resp = e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
}

func TestOIDCNotProvisioned(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"auto_create_users": false})
	_, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/login?error=not_provisioned")
	if hasSession(resp) {
		t.Error("session cookie on a refusal")
	}
}

func TestOIDCLinkByEmail(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"link_by_email": true})
	ctx := context.Background()
	bob, err := e.ts.Store.CreateUser(ctx, store.User{Username: "bob", Email: "B@x.test"})
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{"sub": "bob-sub", "email": "b@x.test", "email_verified": true, "preferred_username": "bob"}

	// Unverified email: no link, a fresh user with a free name, and no email stored.
	unv := map[string]any{"sub": "other-sub", "email": "b@x.test", "email_verified": false, "preferred_username": "bob"}
	c, resp := e.login(t, unv)
	wantRedirect(t, resp, "/")
	if _, me := e.me(t, c); me.User.Username != "bob-2" || me.User.ID == bob.ID || me.User.Email != "" {
		t.Fatalf("unverified: %+v", me.User)
	}

	c, resp = e.login(t, claims)
	wantRedirect(t, resp, "/")
	_, me := e.me(t, c)
	if me.User.ID != bob.ID || !me.User.OIDC {
		t.Fatalf("verified link: %+v", me.User)
	}
	if got, _ := e.ts.Store.UserByID(ctx, bob.ID); got.Sub != "bob-sub" || got.Iss != e.idp.Issuer {
		t.Errorf("bob = %+v", got)
	}
}

func TestOIDCLinkByEmailDisabledOrAmbiguous(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"link_by_email": true})
	ctx := context.Background()
	d, _ := e.ts.Store.CreateUser(ctx, store.User{Username: "dis", Email: "d@x.test", Disabled: true})
	c1, _ := e.ts.Store.CreateUser(ctx, store.User{Username: "twin1", Email: "t@x.test"})
	e.ts.Store.CreateUser(ctx, store.User{Username: "twin2", Email: "T@x.test"})

	_, resp := e.login(t, map[string]any{"sub": "d", "email": "d@x.test", "email_verified": true})
	wantRedirect(t, resp, "/login?error=inactive")
	if got, _ := e.ts.Store.UserByID(ctx, d.ID); got.Iss != "" {
		t.Error("a disabled user was linked")
	}
	c, resp := e.login(t, map[string]any{"sub": "t", "email": "t@x.test", "email_verified": true, "preferred_username": "tt"})
	wantRedirect(t, resp, "/")
	_, me := e.me(t, c)
	if me.User.ID == c1.ID || me.User.Username != "tt" || me.User.Email != "" {
		t.Errorf("ambiguous email linked or stored it: %+v", me.User)
	}
	if got, _ := e.ts.Store.UserByID(ctx, c1.ID); got.Iss != "" {
		t.Error("ambiguous email linked a user")
	}
}

func TestOIDCUsernameDerivation(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	for i, tc := range []struct {
		claims map[string]any
		want   string
	}{
		{map[string]any{"sub": "s1", "preferred_username": "Jane  Doe"}, "Jane-Doe"},
		{map[string]any{"sub": "s2", "email": "kim@x.test"}, "kim"},
		{map[string]any{"sub": "s3"}, "s3"},
		{map[string]any{"sub": "s4", "preferred_username": "jane-doe"}, "jane-doe-2"}, // case-insensitive clash
		{map[string]any{"sub": "s5", "preferred_username": "admin"}, "admin-2"},       // the bootstrap admin
	} {
		c, resp := e.login(t, tc.claims)
		wantRedirect(t, resp, "/")
		if _, me := e.me(t, c); me.User.Username != tc.want {
			t.Errorf("case %d: username %q, want %q", i, me.User.Username, tc.want)
		}
	}
}

func TestOIDCStateMismatch(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	e.ts.addProvider(t, e.admin, e.idp, "b", nil)

	// A tampered state query parameter.
	c := e.ts.client(t)
	cb := e.ts.oidcStart(t, c, e.idp, "authentik", "", riderClaims())
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Set("state", "tampered")
	u.RawQuery = q.Encode()
	resp, err := c.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, resp, "/login?error=state")

	// A cookie issued for slug "authentik" replayed on slug "b".
	c = e.ts.client(t)
	cb = e.ts.oidcStart(t, c, e.idp, "authentik", "", riderClaims())
	resp, err = c.Get(strings.Replace(cb, "/oidc/authentik/", "/oidc/b/", 1))
	if err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, resp, "/login?error=state")
	if hasSession(resp) {
		t.Error("session on a state failure")
	}

	// No cookie at all.
	cb = e.ts.oidcStart(t, e.ts.client(t), e.idp, "authentik", "", riderClaims())
	resp, err = e.ts.client(t).Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, resp, "/login?error=state")

	// The state cookie is single use: replaying the callback after success fails.
	c = e.ts.client(t)
	cb = e.ts.oidcStart(t, c, e.idp, "authentik", "", riderClaims())
	if resp, _ = c.Get(cb); resp.Header.Get("Location") != "/" {
		t.Fatalf("first callback: %v", resp.Header)
	}
	resp, _ = e.ts.client(t).Get(cb)
	wantRedirect(t, resp, "/login?error=state")
}

func TestOIDCNonceMismatch(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	e.idp.ForceNonce("x")
	_, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/login?error=exchange_failed")
	if hasSession(resp) {
		t.Error("session on a nonce mismatch")
	}
}

func TestOIDCProviderError(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	c := e.ts.client(t)
	e.ts.oidcStart(t, c, e.idp, "authentik", "", riderClaims())
	resp, _ := e.ts.do(t, c, "GET", "/api/auth/oidc/authentik/callback?error=access_denied&error_description=nope", nil)
	wantRedirect(t, resp, "/login?error=provider_error")
}

func TestAdminSync(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"admin_group": "wattson-admins"})
	claims := riderClaims()
	claims["groups"] = []any{"riders", "wattson-admins"}
	c, _ := e.login(t, claims)
	if _, me := e.me(t, c); !me.User.IsAdmin {
		t.Fatal("admin group member is not admin")
	}
	claims["groups"] = []any{"riders"}
	c, _ = e.login(t, claims)
	if _, me := e.me(t, c); me.User.IsAdmin {
		t.Fatal("admin flag kept after leaving the group")
	}
}

func TestAdminSyncUnmanagedKeepsFlag(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil) // no admin_group
	c, _ := e.login(t, riderClaims())
	_, me := e.me(t, c)
	f := true
	if _, err := e.ts.Store.UpdateUserFlags(context.Background(), me.User.ID, &f, nil); err != nil {
		t.Fatal(err)
	}
	c, _ = e.login(t, riderClaims())
	if _, me := e.me(t, c); !me.User.IsAdmin {
		t.Error("an unmanaged admin flag was reset")
	}
}

func TestAdminSyncProtectsBootstrapAndLastAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newFlow(t, map[string]any{"admin_group": "wattson-admins", "link_by_email": true})
	// Link the bootstrap admin to the IdP; leaving the group must not demote.
	boot, _ := e.ts.Store.UserByUsername(ctx, "admin")
	if err := e.ts.Store.LinkOIDC(ctx, boot.ID, e.idp.Issuer, "boot-sub"); err != nil {
		t.Fatal(err)
	}
	c, resp := e.login(t, map[string]any{"sub": "boot-sub", "groups": []any{"riders"}})
	wantRedirect(t, resp, "/")
	if _, me := e.me(t, c); me.User.ID != boot.ID || !me.User.IsAdmin {
		t.Fatalf("bootstrap admin demoted: %+v", me.User)
	}
	if !strings.Contains(e.ts.Log.String(), "oidc admin sync skipped") {
		t.Error("skipped sync not logged")
	}

	// A second admin gets demoted only while another admin remains.
	adm := true
	u, _ := e.ts.Store.CreateUser(ctx, store.User{Username: "eve", Iss: e.idp.Issuer, Sub: "eve-sub", IsAdmin: adm})
	_, resp = e.login(t, map[string]any{"sub": "eve-sub", "groups": []any{}})
	wantRedirect(t, resp, "/")
	if got, _ := e.ts.Store.UserByID(ctx, u.ID); got.IsAdmin {
		t.Error("eve kept admin although another admin exists")
	}
}

func TestAdminSyncLastAdminKept(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ctx := context.Background()
	idp := oidctest.NewIDP(t)
	// No bootstrap user: an OIDC-linked admin is the only admin.
	only, err := ts.Store.CreateUser(ctx, store.User{Username: "only", Iss: idp.Issuer, Sub: "only-sub", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	prov := ts.Store
	box := ts.Box
	if _, err := prov.CreateOIDCProvider(ctx, store.OIDCProvider{Slug: "authentik", DisplayName: "A", IssuerURL: idp.DiscoveryURL,
		ClientID: idp.ClientID, ClientSecretEnc: box.SealFor(idp.ClientSecret, providerSecretAAD("authentik")),
		Scopes: "openid", GroupsClaim: "groups", AdminGroup: "wattson-admins", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c := ts.client(t)
	resp := ts.oidcLogin(t, c, idp, "authentik", "", map[string]any{"sub": "only-sub", "groups": []any{}})
	wantRedirect(t, resp, "/")
	if got, _ := ts.Store.UserByID(ctx, only.ID); !got.IsAdmin {
		t.Error("the last admin was demoted")
	}
}

func TestOIDCDisabledUser(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	_, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
	u, err := e.ts.Store.UserByIssSub(context.Background(), e.idp.Issuer, "u1")
	if err != nil {
		t.Fatal(err)
	}
	d := true
	if _, err := e.ts.Store.UpdateUserFlags(context.Background(), u.ID, nil, &d); err != nil {
		t.Fatal(err)
	}
	c, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/login?error=inactive")
	if hasSession(resp) {
		t.Error("session for a disabled user")
	}
	if code, _ := e.me(t, c); code != 401 {
		t.Errorf("me = %d, want 401", code)
	}
}

func TestProviderEditInvalidatesCache(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	_, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
	if got := e.idp.LastAuthorize().Get("client_id"); got != e.idp.ClientID {
		t.Fatalf("client_id = %q", got)
	}
	p, _ := e.ts.Store.OIDCProviderBySlug(context.Background(), "authentik")
	resp2, b := e.ts.do(t, e.admin, "PATCH", fmt.Sprintf("%s/%d", providersPath, p.ID), map[string]any{"client_id": "new-client"})
	if resp2.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp2.StatusCode, b)
	}
	c := e.ts.client(t)
	r := e.ts.oidcLogin(t, c, e.idp, "authentik", "", riderClaims())
	wantRedirect(t, r, "/")
	if got := e.idp.LastAuthorize().Get("client_id"); got != "new-client" {
		t.Errorf("client_id after edit = %q, want new-client", got)
	}
}

func TestDiscoveryCachedAcrossLogins(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	e.login(t, riderClaims())
	e.login(t, riderClaims())
	// Per login the issuer is resolved once at login and once at callback;
	// go-oidc's own discovery ran once for the first.
	if n := e.idp.Requests("/.well-known/openid-configuration"); n != 5 {
		t.Errorf("discovery requests = %d, want 5", n)
	}
	if n := e.idp.Requests("/jwks"); n != 1 {
		t.Errorf("jwks requests = %d, want 1", n)
	}
}

func TestOIDCAuthentikIssuerWithTrailingSlash(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil, oidctest.WithIssuerPath("/application/o/wattson/"))
	c, resp := e.login(t, riderClaims())
	wantRedirect(t, resp, "/")
	if _, me := e.me(t, c); me.User.Username != "alice" {
		t.Fatalf("me = %+v", me.User)
	}
	u, err := e.ts.Store.UserByIssSub(context.Background(), e.idp.URL+"/application/o/wattson/", "u1")
	if err != nil || u.Username != "alice" {
		t.Fatalf("user stored under the discovery issuer: %+v, %v", u, err)
	}
}

func TestUnknownAndUnavailableProvider(t *testing.T) {
	t.Parallel()
	e := newFlow(t, nil)
	c := e.ts.client(t)
	resp, _ := e.ts.do(t, c, "GET", "/api/auth/oidc/nope/login", nil)
	wantRedirect(t, resp, "/login?error=unknown_provider")
	resp, _ = e.ts.do(t, c, "GET", "/api/auth/oidc/nope/callback?code=x&state=y", nil)
	wantRedirect(t, resp, "/login?error=state")

	p, _ := e.ts.Store.OIDCProviderBySlug(context.Background(), "authentik")
	off := false
	if resp, b := e.ts.do(t, e.admin, "PATCH", fmt.Sprintf("%s/%d", providersPath, p.ID), map[string]any{"enabled": off}); resp.StatusCode != 200 {
		t.Fatalf("disable: %d %s", resp.StatusCode, b)
	}
	resp, _ = e.ts.do(t, c, "GET", "/api/auth/oidc/authentik/login", nil)
	wantRedirect(t, resp, "/login?error=unknown_provider")

	if resp, b := e.ts.do(t, e.admin, "PATCH", fmt.Sprintf("%s/%d", providersPath, p.ID),
		map[string]any{"enabled": true, "discovery_url": "http://127.0.0.1:1"}); resp.StatusCode != 200 {
		t.Fatalf("repoint: %d %s", resp.StatusCode, b)
	}
	resp, _ = e.ts.do(t, c, "GET", "/api/auth/oidc/authentik/login", nil)
	wantRedirect(t, resp, "/login?error=provider_unavailable")
}

func TestOIDCNoSecretsInLogs(t *testing.T) {
	t.Parallel()
	e := newFlow(t, map[string]any{"allowed_groups": []string{"nobody"}})
	e.login(t, riderClaims())
	logs := e.ts.Log.String()
	if strings.Contains(logs, e.idp.ClientSecret) || strings.Contains(logs, "id_token") || strings.Contains(logs, "test-access-token") {
		t.Errorf("secret or token in logs: %s", logs)
	}
}
