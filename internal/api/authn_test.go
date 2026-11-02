package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/settings"
)

func TestLoginLogout(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	c := ts.client(t)

	resp, b := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": "wrong-password"})
	if resp.StatusCode != 401 || errCode(t, b) != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", resp.StatusCode, b)
	}
	// Unknown user, wrong password and a password-less user are indistinguishable.
	resp2, b2 := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "nobody", "password": "wrong-password"})
	if resp2.StatusCode != 401 || string(b2) != string(b) {
		t.Errorf("unknown user: %d %s, want same as wrong password %s", resp2.StatusCode, b2, b)
	}
	resp, b = ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "BOB", "password": "bob-password-1"})
	if resp.StatusCode != 204 {
		t.Fatalf("login: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, c, "GET", "/api/auth/me", nil); resp.StatusCode != 200 {
		t.Fatalf("me: %d", resp.StatusCode)
	}
	resp, _ = ts.do(t, c, "POST", "/api/auth/logout", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if cs := resp.Cookies(); len(cs) == 0 || cs[0].MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %v", cs)
	}
	if resp, _ = ts.do(t, c, "GET", "/api/auth/me", nil); resp.StatusCode != 401 {
		t.Errorf("me after logout: %d, want 401", resp.StatusCode)
	}
}

func TestLoginRefusals(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ctx := context.Background()
	bob := ts.addUser(t, "bob", "bob-password-1", false)
	// An OIDC-only user has no password and cannot sign in locally.
	if _, err := ts.Store.CreateUser(ctx, storeUser("oidc-only", "https://idp", "sub1")); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 257)
	for _, tc := range []struct{ user, pw string }{
		{"oidc-only", "anything-at-all"}, {"bob", long},
	} {
		resp, b := ts.do(t, nil, "POST", "/api/auth/login", map[string]any{"username": tc.user, "password": tc.pw})
		if resp.StatusCode != 401 || errCode(t, b) != "invalid_credentials" {
			t.Errorf("%s: %d %s", tc.user, resp.StatusCode, b)
		}
	}
	tru := true
	if _, err := ts.Store.UpdateUserFlags(ctx, bob.ID, nil, &tru); err != nil {
		t.Fatal(err)
	}
	resp, b := ts.do(t, nil, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-password-1"})
	if resp.StatusCode != 401 || errCode(t, b) != "invalid_credentials" {
		t.Errorf("disabled: %d %s", resp.StatusCode, b)
	}
}

func TestLoginRateLimited(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	c := ts.client(t)
	for i := range 10 {
		resp, b := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": "wrong-password"})
		if resp.StatusCode != 401 {
			t.Fatalf("attempt %d: %d %s", i, resp.StatusCode, b)
		}
	}
	// Even the right password is refused once the key is exhausted, and
	// X-Forwarded-For does not buy a fresh key.
	resp, b := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "Bob", "password": "bob-password-1"},
		"X-Forwarded-For", "203.0.113.9")
	if resp.StatusCode != 429 || errCode(t, b) != "rate_limited" {
		t.Fatalf("11th attempt: %d %s", resp.StatusCode, b)
	}
	// The ten failures took real time, so the wait is a little under the window.
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || secs < 590 || secs > 600 {
		t.Errorf("Retry-After = %q, want 590..600", resp.Header.Get("Retry-After"))
	}
	// The limit is per IP: any other username from the same address is refused too.
	resp, b = ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "alice", "password": "wrong-password"})
	if resp.StatusCode != 429 || errCode(t, b) != "rate_limited" {
		t.Errorf("other username: %d %s, want 429", resp.StatusCode, b)
	}
}

func TestLocalLoginDisabled(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	if _, err := ts.Settings.Update(context.Background(), func(s *settings.Settings) error {
		s.Auth.AllowLocalLogin = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp, b := ts.do(t, nil, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-password-1"})
	if resp.StatusCode != 403 || errCode(t, b) != "local_login_disabled" {
		t.Errorf("bob: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, nil, "POST", "/api/auth/login", map[string]any{"username": "admin", "password": testPW})
	if resp.StatusCode != 204 {
		t.Errorf("bootstrap admin: %d %s", resp.StatusCode, b)
	}
	_, b = ts.do(t, nil, "GET", "/api/auth/providers", nil)
	if p := decode[ProvidersOut](t, b); p.LocalLogin || p.NeedsSetup || p.Providers == nil {
		t.Errorf("providers = %s", b)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	body := map[string]any{"username": "bob", "password": "bob-password-1"}

	resp, _ := ts.do(t, nil, "POST", "/api/auth/login", body)
	ck := sessionCookie(t, resp)
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 604800 || ck.Secure {
		t.Errorf("cookie = %+v", ck)
	}
	resp, _ = ts.do(t, nil, "POST", "/api/auth/login", body, "X-Forwarded-Proto", "https")
	if ck = sessionCookie(t, resp); !ck.Secure {
		t.Errorf("cookie not Secure behind https proxy: %+v", ck)
	}
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "wattson_session" {
			return c
		}
	}
	t.Fatal("no wattson_session cookie")
	return nil
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	old := ts.loginAs(t, "bob", "bob-password-1")

	resp, b := ts.do(t, old, "POST", "/api/auth/password", map[string]any{"current_password": "nope-nope-nope", "new_password": "new-password-22"})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_current_password" {
		t.Fatalf("wrong current: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, old, "POST", "/api/auth/password", map[string]any{"current_password": "bob-password-1", "new_password": "short"})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "password") {
		t.Fatalf("short new: %d %s", resp.StatusCode, b)
	}
	// A second session must die with the change.
	stale := ts.loginAs(t, "bob", "bob-password-1")
	resp, b = ts.do(t, old, "POST", "/api/auth/password", map[string]any{"current_password": "bob-password-1", "new_password": "new-password-22"})
	if resp.StatusCode != 204 {
		t.Fatalf("change: %d %s", resp.StatusCode, b)
	}
	if r, _ := ts.do(t, stale, "GET", "/api/auth/me", nil); r.StatusCode != 401 {
		t.Errorf("other session after change: %d, want 401", r.StatusCode)
	}
	if r, _ := ts.do(t, old, "GET", "/api/auth/me", nil); r.StatusCode != 200 {
		t.Errorf("new cookie: %d, want 200", r.StatusCode)
	}
	ts.loginAs(t, "bob", "new-password-22")
	if r, _ := ts.do(t, nil, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-password-1"}); r.StatusCode != 401 {
		t.Errorf("old password still works: %d", r.StatusCode)
	}
}

func TestChangePasswordStartSessionFailureRevokesSessions(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	bob := ts.addUser(t, "bob", "bob-password-1", false)
	old := ts.loginAs(t, "bob", "bob-password-1")
	other := ts.loginAs(t, "bob", "bob-password-1")
	// A corrupt auth section makes Settings.Get, and so startSession, fail
	// after the password has already been replaced.
	if err := ts.Store.PutSettings(context.Background(), map[string]string{"auth": "{"}); err != nil {
		t.Fatal(err)
	}
	resp, b := ts.do(t, old, "POST", "/api/auth/password", map[string]any{"current_password": "bob-password-1", "new_password": "new-password-22"})
	if resp.StatusCode != 500 {
		t.Fatalf("change: %d %s, want 500", resp.StatusCode, b)
	}
	for name, c := range map[string]*http.Client{"requesting": old, "other": other} {
		if r, _ := ts.do(t, c, "GET", "/api/auth/me", nil); r.StatusCode != 401 {
			t.Errorf("%s session after failed change: %d, want 401", name, r.StatusCode)
		}
	}
	if u, err := ts.Store.UserByUsername(context.Background(), "bob"); err != nil || u.PasswordHash == bob.PasswordHash {
		t.Errorf("password hash unchanged (%v); the test must exercise the post-SetPassword path", err)
	}
}

func TestChangePasswordTokenRefused(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "tokuser", false, "read")
	resp, b := ts.do(t, nil, "POST", "/api/auth/password",
		map[string]any{"current_password": "x", "new_password": "new-password-22"}, "Authorization", "Bearer "+tok)
	if resp.StatusCode != 403 || errCode(t, b) != "session_required" {
		t.Errorf("token: %d %s", resp.StatusCode, b)
	}
}

func TestPasswordChangeCrossOriginRefused(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	c := ts.loginAs(t, "bob", "bob-password-1")
	resp, b := ts.do(t, c, "POST", "/api/auth/password",
		map[string]any{"current_password": "bob-password-1", "new_password": "new-password-22"}, "Origin", "https://evil.example")
	if resp.StatusCode != 403 || errCode(t, b) != "csrf" {
		t.Errorf("cross-origin: %d %s", resp.StatusCode, b)
	}
}
