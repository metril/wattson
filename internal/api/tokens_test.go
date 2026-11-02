package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func bearer(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

func TestTokenLifecycle(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)

	resp, b := ts.do(t, admin, "POST", "/api/tokens", map[string]any{"name": "ha", "scopes": []string{"read", "control"}})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	created := decode[CreatedToken](t, b)
	tok := created.Token
	if !strings.HasPrefix(tok, "wat_") || len(tok) != 47 {
		t.Fatalf("token = %q", tok)
	}
	if created.Name != "ha" || created.Prefix != tok[:12] {
		t.Errorf("created = %+v", created)
	}

	resp, b = ts.do(t, nil, "GET", "/api/auth/me", nil, bearer(tok)...)
	if resp.StatusCode != 200 {
		t.Fatalf("me via token: %d %s", resp.StatusCode, b)
	}
	if me := decode[MeOut](t, b); me.Via != "token" || strings.Join(me.Scopes, ",") != "read,control" {
		t.Errorf("me = %+v", me)
	}

	resp, b = ts.do(t, admin, "GET", "/api/tokens", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}
	list := decode[[]TokenOut](t, b)
	if len(list) != 1 || list[0].Prefix != tok[:12] || list[0].ID != created.ID {
		t.Errorf("list = %+v", list)
	}
	if strings.Contains(string(b), tok) || strings.Contains(string(b), `"token"`) {
		t.Errorf("list leaks the token: %s", b)
	}

	if resp, b = ts.do(t, admin, "DELETE", fmt.Sprintf("/api/tokens/%d", created.ID), nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, nil, "GET", "/api/auth/me", nil, bearer(tok)...); resp.StatusCode != 401 {
		t.Errorf("revoked token: %d, want 401", resp.StatusCode)
	}
	resp, b = ts.do(t, admin, "DELETE", fmt.Sprintf("/api/tokens/%d", created.ID), nil)
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("second delete: %d %s", resp.StatusCode, b)
	}
}

func TestDeleteTokenNeedsControl(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	mk := func(name string, scopes ...string) CreatedToken {
		resp, b := ts.do(t, admin, "POST", "/api/tokens", map[string]any{"name": name, "scopes": scopes})
		if resp.StatusCode != 201 {
			t.Fatalf("create %s: %d %s", name, resp.StatusCode, b)
		}
		return decode[CreatedToken](t, b)
	}
	kiosk, driver := mk("kiosk", "read"), mk("driver", "read", "control")
	path := fmt.Sprintf("/api/tokens/%d", kiosk.ID)
	// A read-only token cannot revoke, not even itself.
	resp, b := ts.do(t, nil, "DELETE", path, nil, bearer(kiosk.Token)...)
	if resp.StatusCode != 403 || errCode(t, b) != "forbidden" {
		t.Fatalf("read token delete: %d %s, want 403 forbidden", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, nil, "GET", "/api/auth/me", nil, bearer(kiosk.Token)...); resp.StatusCode != 200 {
		t.Errorf("kiosk token after refused delete: %d, want still valid", resp.StatusCode)
	}
	if resp, b = ts.do(t, nil, "DELETE", path, nil, bearer(driver.Token)...); resp.StatusCode != 204 {
		t.Errorf("control token delete: %d %s, want 204", resp.StatusCode, b)
	}
}

func TestTokenCannotMintToken(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "svc", true, "read", "control", "admin")
	resp, b := ts.do(t, nil, "POST", "/api/tokens", map[string]any{"name": "x", "scopes": []string{"read"}}, bearer(tok)...)
	if resp.StatusCode != 403 || errCode(t, b) != "session_required" {
		t.Errorf("bearer mint: %d %s", resp.StatusCode, b)
	}
}

func TestNonAdminCannotMintAdmin(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	resp, b := ts.do(t, bob, "POST", "/api/tokens", map[string]any{"name": "x", "scopes": []string{"admin"}})
	if resp.StatusCode != 403 || errCode(t, b) != "scope_exceeds" {
		t.Errorf("admin scope: %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, bob, "POST", "/api/tokens", map[string]any{"name": "x", "scopes": []string{"read", "control"}}); resp.StatusCode != 201 {
		t.Errorf("own scopes: %d %s", resp.StatusCode, b)
	}
}

func TestTokenValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"empty name", map[string]any{"name": "", "scopes": []string{"read"}}},
		{"long name", map[string]any{"name": strings.Repeat("n", 101), "scopes": []string{"read"}}},
		{"no scopes", map[string]any{"name": "x", "scopes": []string{}}},
		{"unknown scope", map[string]any{"name": "x", "scopes": []string{"root"}}},
	} {
		resp, b := ts.do(t, admin, "POST", "/api/tokens", tc.body)
		if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" {
			t.Errorf("%s: %d %s, want 400", tc.name, resp.StatusCode, b)
		}
	}
}

func TestTokensAreOwnedByCaller(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	_, b := ts.do(t, admin, "POST", "/api/tokens", map[string]any{"name": "mine", "scopes": []string{"read"}})
	id := decode[CreatedToken](t, b).ID

	_, b = ts.do(t, bob, "GET", "/api/tokens", nil)
	if l := decode[[]TokenOut](t, b); len(l) != 0 || !strings.HasPrefix(string(b), "[") {
		t.Errorf("bob sees %s", b)
	}
	if resp, _ := ts.do(t, bob, "DELETE", fmt.Sprintf("/api/tokens/%d", id), nil); resp.StatusCode != 404 {
		t.Errorf("bob deletes admin's token: %d, want 404", resp.StatusCode)
	}
}

func TestTokenLastUsed(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, b := ts.do(t, admin, "POST", "/api/tokens", map[string]any{"name": "ha", "scopes": []string{"read"}})
	tok := decode[CreatedToken](t, b).Token
	if l := decode[[]TokenOut](t, mustList(t, ts, admin)); l[0].LastUsedAt != nil {
		t.Fatalf("last_used_at before use: %v", l[0].LastUsedAt)
	}
	if resp, _ := ts.do(t, nil, "GET", "/api/auth/me", nil, bearer(tok)...); resp.StatusCode != 200 {
		t.Fatalf("bearer call: %d", resp.StatusCode)
	}
	if l := decode[[]TokenOut](t, mustList(t, ts, admin)); l[0].LastUsedAt == nil {
		t.Error("last_used_at still null after a bearer call")
	}
}

func mustList(t *testing.T, ts *testServer, c *http.Client) []byte {
	t.Helper()
	resp, b := ts.do(t, c, "GET", "/api/tokens", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}
	return b
}
