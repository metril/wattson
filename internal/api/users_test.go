package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/store"
)

func storeUser(name, iss, sub string) store.User {
	return store.User{Username: name, Iss: iss, Sub: sub}
}

func TestUsersAdmin(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	bobU := ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")

	if resp, b := ts.do(t, bob, "GET", "/api/users", nil); resp.StatusCode != 403 {
		t.Errorf("bob list: %d %s, want 403", resp.StatusCode, b)
	}
	resp, b := ts.do(t, admin, "GET", "/api/users", nil)
	if resp.StatusCode != 200 || len(decode[[]UserOut](t, b)) != 2 {
		t.Fatalf("admin list: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", "/api/users/1", map[string]any{"is_admin": false})
	if resp.StatusCode != 409 || errCode(t, b) != "bootstrap_protected" {
		t.Errorf("demote bootstrap: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", "/api/users/1", map[string]any{"disabled": true})
	if resp.StatusCode != 409 || errCode(t, b) != "bootstrap_protected" {
		t.Errorf("disable bootstrap: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", "/api/users/999", map[string]any{"disabled": true})
	if resp.StatusCode != 404 {
		t.Errorf("missing user: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", fmt.Sprintf("/api/users/%d", bobU.ID), map[string]any{"disabled": true})
	if resp.StatusCode != 200 || !decode[UserOut](t, b).Disabled {
		t.Fatalf("disable bob: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, bob, "GET", "/api/auth/me", nil); resp.StatusCode != 401 {
		t.Errorf("bob cookie after disable: %d, want 401", resp.StatusCode)
	}
	if n, _ := ts.Store.DeleteUserSessions(t.Context(), bobU.ID); n != 0 {
		t.Errorf("disabling left %d sessions", n)
	}
}

func TestLastAdminProtected(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	// Two admins, neither bootstrap: demoting the second-to-last works, the last does not.
	a := ts.addUser(t, "a", "password-aaaa-1", true)
	b := ts.addUser(t, "b", "password-bbbb-1", true)
	ca := ts.loginAs(t, "a", "password-aaaa-1")
	resp, body := ts.do(t, ca, "PATCH", fmt.Sprintf("/api/users/%d", b.ID), map[string]any{"is_admin": false})
	if resp.StatusCode != 200 {
		t.Fatalf("demote b: %d %s", resp.StatusCode, body)
	}
	resp, body = ts.do(t, ca, "PATCH", fmt.Sprintf("/api/users/%d", a.ID), map[string]any{"is_admin": false})
	if resp.StatusCode != 409 || errCode(t, body) != "last_admin" {
		t.Errorf("demote last admin: %d %s", resp.StatusCode, body)
	}
	resp, body = ts.do(t, ca, "PATCH", fmt.Sprintf("/api/users/%d", a.ID), map[string]any{"disabled": true})
	if resp.StatusCode != 409 || errCode(t, body) != "last_admin" {
		t.Errorf("disable last admin: %d %s", resp.StatusCode, body)
	}
}

func TestUpdateOwnDisplayName(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.setupAdmin(t)
	u := ts.addUser(t, "carol", "carol-password-1", false)
	if _, err := ts.Store.UpdateDisplayName(t.Context(), u.ID, "Carol Old"); err != nil {
		t.Fatal(err)
	}
	c := ts.loginAs(t, "carol", "carol-password-1")
	// Default rider carries the display name; a second one is custom.
	if resp, b := ts.do(t, c, "GET", "/api/riders", nil); resp.StatusCode != 200 || decode[[]RiderOut](t, b)[0].Name != "Carol Old" {
		t.Fatalf("riders: %d %s", resp.StatusCode, b)
	}
	if resp, b := ts.do(t, c, "POST", "/api/riders", map[string]any{"name": "Tandem"}); resp.StatusCode != 201 {
		t.Fatalf("create rider: %d %s", resp.StatusCode, b)
	}
	resp, b := ts.do(t, c, "PATCH", "/api/auth/me", map[string]any{"display_name": "  Carol New  "})
	if resp.StatusCode != 200 || decode[MeOut](t, b).User.DisplayName != "Carol New" {
		t.Fatalf("patch me: %d %s", resp.StatusCode, b)
	}
	_, b = ts.do(t, c, "GET", "/api/riders", nil)
	names := map[string]bool{}
	for _, r := range decode[[]RiderOut](t, b) {
		names[r.Name] = true
	}
	if !names["Carol New"] || !names["Tandem"] || names["Carol Old"] {
		t.Errorf("rider names after rename: %v", names)
	}
	_, b = ts.do(t, c, "GET", "/api/auth/me", nil)
	if decode[MeOut](t, b).User.DisplayName != "Carol New" {
		t.Errorf("me after rename: %s", b)
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", 65)} {
		if resp, b := ts.do(t, c, "PATCH", "/api/auth/me", map[string]any{"display_name": name}); resp.StatusCode != 400 {
			t.Errorf("name %q: %d %s, want 400", name, resp.StatusCode, b)
		}
	}
}
