package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/oidcauth/oidctest"
)

const providersPath = "/api/admin/oidc-providers"

// addProvider creates a provider through the admin API against idp.
func (ts *testServer) addProvider(t *testing.T, admin *http.Client, idp *oidctest.IDP, slug string, extra map[string]any) ProviderOut {
	t.Helper()
	body := map[string]any{"slug": slug, "display_name": strings.ToUpper(slug[:1]) + slug[1:], "discovery_url": idp.DiscoveryURL,
		"client_id": idp.ClientID, "client_secret": idp.ClientSecret}
	for k, v := range extra {
		body[k] = v
	}
	resp, b := ts.do(t, admin, "POST", providersPath, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider: %d %s", resp.StatusCode, b)
	}
	return decode[ProviderOut](t, b)
}

func TestProviderCRUD(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	const secret = "s3cret-value"

	resp, b := ts.do(t, admin, "POST", providersPath, map[string]any{"slug": "authentik", "display_name": "Authentik",
		"discovery_url": "https://auth.example/application/o/wattson/", "client_id": "wattson", "client_secret": secret})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	p := decode[ProviderOut](t, b)
	if !p.ClientSecretSet || p.RedirectURI != ts.URL+"/api/auth/oidc/authentik/callback" {
		t.Fatalf("created: %+v", p)
	}
	if p.Scopes != "openid profile email groups" || p.GroupsClaim != "groups" || !p.AutoCreateUsers || p.LinkByEmail || !p.Enabled ||
		p.AllowedGroups == nil || p.AllowedEmails == nil {
		t.Fatalf("defaults: %+v", p)
	}
	if strings.Contains(string(b), secret) || !strings.Contains(string(b), `"client_secret_set":true`) {
		t.Errorf("create body leaks the secret or lacks the flag: %s", b)
	}

	path := fmt.Sprintf("%s/%d", providersPath, p.ID)
	for _, path := range []string{path, providersPath} {
		_, b := ts.do(t, admin, "GET", path, nil)
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "client_secret_enc") {
			t.Errorf("GET %s leaks: %s", path, b)
		}
	}
	// The stored value is sealed, bound to the slug, and not the plaintext.
	row, _ := ts.Store.OIDCProviderBySlug(context.Background(), "authentik")
	if row.ClientSecretEnc == "" || strings.Contains(row.ClientSecretEnc, secret) {
		t.Errorf("stored secret = %q", row.ClientSecretEnc)
	}
	if got, err := ts.Box.OpenFor(row.ClientSecretEnc, providerSecretAAD("authentik")); err != nil || got != secret {
		t.Errorf("open sealed secret: %q, %v", got, err)
	}
	if _, err := ts.Box.OpenFor(row.ClientSecretEnc, providerSecretAAD("other")); err == nil {
		t.Error("sealed secret opens under another slug's AAD")
	}

	resp, b = ts.do(t, admin, "POST", providersPath, map[string]any{"slug": "authentik", "display_name": "Dup",
		"discovery_url": "https://x.example", "client_id": "c", "client_secret": "s"})
	if resp.StatusCode != 409 || errCode(t, b) != "slug_taken" {
		t.Errorf("duplicate slug: %d %s", resp.StatusCode, b)
	}
	for _, bad := range []map[string]any{
		{"slug": "Auth Entik", "display_name": "x", "discovery_url": "https://x.example", "client_id": "c", "client_secret": "s"},
		{"slug": "-a", "display_name": "x", "discovery_url": "https://x.example", "client_id": "c", "client_secret": "s"},
		{"slug": "ok", "display_name": "x", "discovery_url": "ftp://x.example", "client_id": "c", "client_secret": "s"},
		{"slug": "ok", "display_name": "x", "discovery_url": "https://x.example", "client_id": "c"},
		{"slug": "ok", "display_name": "  ", "discovery_url": "https://x.example", "client_id": "c", "client_secret": "s"},
	} {
		if resp, b := ts.do(t, admin, "POST", providersPath, bad); resp.StatusCode != 400 {
			t.Errorf("create %v: %d %s, want 400", bad, resp.StatusCode, b)
		}
	}

	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"display_name": "Wattson SSO", "allowed_groups": []string{" riders "}})
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	up := decode[ProviderOut](t, b)
	if up.DisplayName != "Wattson SSO" || len(up.AllowedGroups) != 1 || up.AllowedGroups[0] != "riders" || up.Slug != "authentik" ||
		!up.ClientSecretSet || !up.UpdatedAt.After(p.UpdatedAt) {
		t.Errorf("patched: %+v", up)
	}
	// An empty secret is rejected and does not clear the stored one; omitting keeps it.
	if resp, b := ts.do(t, admin, "PATCH", path, map[string]any{"client_secret": ""}); resp.StatusCode != 400 {
		t.Errorf("empty secret: %d %s", resp.StatusCode, b)
	}
	if row2, _ := ts.Store.OIDCProviderBySlug(context.Background(), "authentik"); row2.ClientSecretEnc != row.ClientSecretEnc {
		t.Error("PATCH without a secret changed the stored one")
	}
	if resp, _ := ts.do(t, admin, "PATCH", path, map[string]any{"client_secret": "new-secret"}); resp.StatusCode != 200 {
		t.Fatal("patch secret failed")
	}
	row2, _ := ts.Store.OIDCProviderBySlug(context.Background(), "authentik")
	if got, _ := ts.Box.OpenFor(row2.ClientSecretEnc, providerSecretAAD("authentik")); got != "new-secret" {
		t.Errorf("replaced secret = %q", got)
	}
	// [] clears a list.
	_, b = ts.do(t, admin, "PATCH", path, map[string]any{"allowed_groups": []string{}})
	if g := decode[ProviderOut](t, b).AllowedGroups; len(g) != 0 || g == nil {
		t.Errorf("cleared groups = %#v", g)
	}

	if resp, _ := ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if resp, _ := ts.do(t, admin, m, path, map[string]any{}); resp.StatusCode != 404 {
			t.Errorf("%s after delete: %d, want 404", m, resp.StatusCode)
		}
	}
	if strings.Contains(ts.Log.String(), secret) || strings.Contains(ts.Log.String(), "new-secret") {
		t.Error("a secret reached the log")
	}
}

func TestProviderAdminOnly(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	body := map[string]any{"slug": "a", "display_name": "A", "discovery_url": "https://x.example", "client_id": "c", "client_secret": "s"}
	for _, tc := range []struct{ method, path string }{
		{"GET", providersPath}, {"POST", providersPath}, {"GET", providersPath + "/1"},
		{"PATCH", providersPath + "/1"}, {"DELETE", providersPath + "/1"}, {"POST", providersPath + "/discover"},
	} {
		if resp, b := ts.do(t, bob, tc.method, tc.path, body); resp.StatusCode != 403 {
			t.Errorf("bob %s %s: %d %s, want 403", tc.method, tc.path, resp.StatusCode, b)
		}
		if resp, b := ts.do(t, nil, tc.method, tc.path, body); resp.StatusCode != 401 {
			t.Errorf("anon %s %s: %d %s, want 401", tc.method, tc.path, resp.StatusCode, b)
		}
	}
}

func TestRedirectURIUsesPublicURL(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"general": map[string]any{"public_url": "https://wattson.example"}}); resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	p := ts.addProvider(t, admin, oidctest.NewIDP(t), "authentik", nil)
	if want := "https://wattson.example/api/auth/oidc/authentik/callback"; p.RedirectURI != want {
		t.Errorf("redirect_uri = %q, want %q", p.RedirectURI, want)
	}
}

func TestProvidersPublicList(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idp := oidctest.NewIDP(t)
	ts.addProvider(t, admin, idp, "zeta", map[string]any{"display_name": "Zeta"})
	ts.addProvider(t, admin, idp, "off", map[string]any{"display_name": "Off", "enabled": false})
	ts.addProvider(t, admin, idp, "authentik", map[string]any{"display_name": "alpha"})

	resp, b := ts.do(t, nil, "GET", "/api/auth/providers", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	got := decode[ProvidersOut](t, b).Providers
	if len(got) != 2 || got[0].Slug != "authentik" || got[0].LoginURL != "/api/auth/oidc/authentik/login" || got[1].Slug != "zeta" {
		t.Errorf("providers = %+v, want enabled only, ordered by display name", got)
	}
}

func TestDiscoverEndpoint(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idp := oidctest.NewIDP(t, oidctest.WithIssuerPath("/application/o/wattson/"))
	post := func(u string) TestResult {
		resp, b := ts.do(t, admin, "POST", providersPath+"/discover", map[string]any{"discovery_url": u})
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		return decode[TestResult](t, b)
	}
	for _, u := range []string{idp.Issuer, idp.DiscoveryURL} {
		if r := post(u); !r.OK || r.Message != "issuer "+idp.Issuer {
			t.Errorf("discover %q = %+v", u, r)
		}
	}
	if r := post("http://127.0.0.1:1"); r.OK || r.Message == "" {
		t.Errorf("dead host = %+v", r)
	}
	if r := post("not a url"); r.OK {
		t.Errorf("bad url = %+v", r)
	}
}
