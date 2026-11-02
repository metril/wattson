package cli

import (
	"context"
	"net/http/cookiejar"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

func TestAdminResetPassword(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jar, _ := cookiejar.New(nil)
	s := startServer(t, dir, nil, jar)
	s.setup(t)
	if code, b := s.do(t, "GET", "/api/auth/me", nil); code != 200 {
		t.Fatalf("me before: %d %s", code, b)
	}

	// Runs while the server is up (WAL + busy timeout).
	code, out, errb := runCLI(t, deps{}, "admin", "--data-dir", dir, "reset-password", "admin")
	if code != 0 || !regexp.MustCompile(`^password for admin: [A-Za-z0-9_-]{24}\n$`).MatchString(out) {
		t.Fatalf("reset-password: exit %d, stdout %q, stderr %q", code, out, errb)
	}
	pw := strings.TrimSuffix(strings.TrimPrefix(out, "password for admin: "), "\n")

	if code, b := s.do(t, "GET", "/api/auth/me", nil); code != 401 {
		t.Errorf("old session: %d %s, want 401", code, b)
	}
	if code, _ := s.do(t, "POST", "/api/auth/login", map[string]any{"username": "admin", "password": testPassword}); code != 401 {
		t.Errorf("old password: %d, want 401", code)
	}
	if code, b := s.do(t, "POST", "/api/auth/login", map[string]any{"username": "admin", "password": pw}); code != 204 {
		t.Errorf("login with new password: %d %s, want 204", code, b)
	}

	code, out, errb = runCLI(t, deps{}, "admin", "reset-password", "nobody", "--data-dir", dir) // flag after the user
	if code != 1 || out != "" || !strings.Contains(errb, "no such user: nobody") {
		t.Errorf("unknown user: exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestAdminResetPasswordWarnings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()
	box, err := secretbox.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "wattson.db"))
	if err != nil {
		t.Fatal(err)
	}
	boot, err := st.CreateBootstrapAdmin(ctx, "root", "Root", "hash")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []store.User{{Username: "gone", Disabled: true}, {Username: "bob"}} {
		if _, err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	run := func(user string) string {
		code, out, errb := runCLI(t, deps{}, "admin", "--data-dir", dir, "reset-password", user)
		if code != 0 || !strings.HasPrefix(out, "password for "+user+": ") {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", user, code, out, errb)
		}
		return errb
	}
	// Local login on (default): only a disabled user draws a warning.
	if e := run("bob"); e != "" {
		t.Errorf("enabled user, local login on: stderr %q, want none", e)
	}
	if e := run("gone"); !strings.Contains(e, "disabled") {
		t.Errorf("disabled user: stderr %q, want a disabled warning", e)
	}
	st, err = store.Open(filepath.Join(dir, "wattson.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.NewService(st, box).Update(ctx, func(s *settings.Settings) error {
		s.Auth.AllowLocalLogin = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if e := run("bob"); !strings.Contains(e, "password sign-in is turned off") || !strings.Contains(e, "disable-oidc") {
		t.Errorf("non-bootstrap, local login off: stderr %q, want the disable-oidc hint", e)
	}
	if e := run(boot.Username); e != "" {
		t.Errorf("bootstrap admin, local login off: stderr %q, want none", e)
	}
}

func TestAdminUsageAndMissingDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, args := range [][]string{{"admin"}, {"admin", "bogus"}, {"admin", "reset-password"}, {"admin", "disable-oidc", "extra"}} {
		if code, _, errb := runCLI(t, deps{}, append(args, "--data-dir", dir)...); code != 2 || !strings.Contains(errb, "usage: wattson admin") {
			t.Errorf("%v: exit %d, %q", args, code, errb)
		}
	}
	code, _, errb := runCLI(t, deps{}, "admin", "--data-dir", dir, "disable-oidc")
	if code != 1 || !strings.Contains(errb, "no database at") {
		t.Errorf("missing db: exit %d, %q", code, errb)
	}
	if _, err := store.Open(filepath.Join(dir, "other.db")); err != nil { // sanity: dir usable, nothing created by admin
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "wattson.db*")); len(m) != 0 {
		t.Errorf("admin created %v", m)
	}
}

func TestAdminDisableOIDC(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()
	box, err := secretbox.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "wattson.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"authentik", "keycloak"} {
		if _, err := st.CreateOIDCProvider(ctx, store.OIDCProvider{Slug: slug, DisplayName: slug, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	svc := settings.NewService(st, box)
	if _, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.Auth.AllowLocalLogin = false
		s.Auth.SessionTTLHours = 24
		s.HA.Token = "keep-me"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	code, out, errb := runCLI(t, deps{}, "admin", "--data-dir", dir, "disable-oidc")
	if code != 0 || out != "disabled 2 OIDC providers; local login enabled\n" {
		t.Fatalf("disable-oidc: exit %d, stdout %q, stderr %q", code, out, errb)
	}

	st, err = store.Open(filepath.Join(dir, "wattson.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ps, err := st.ListOIDCProviders(ctx)
	if err != nil || len(ps) != 2 || ps[0].Enabled || ps[1].Enabled {
		t.Errorf("providers = %+v, %v; want both disabled", ps, err)
	}
	got, err := settings.NewService(st, box).Get(ctx)
	if err != nil || !got.Auth.AllowLocalLogin || got.Auth.SessionTTLHours != 24 || got.HA.Token != "keep-me" {
		t.Errorf("settings = %+v, %v; want local login on and everything else untouched", got, err)
	}
	// A second run changes no provider.
	if _, out, _ := runCLI(t, deps{}, "admin", "--data-dir", dir, "disable-oidc"); out != "disabled 0 OIDC providers; local login enabled\n" {
		t.Errorf("second run stdout %q", out)
	}
}
