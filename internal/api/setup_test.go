package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

const testPW = "correct-horse-1"

// setupAdmin runs the setup wizard on ts and returns a client holding the
// admin session.
func (ts *testServer) setupAdmin(t testing.TB) *http.Client {
	t.Helper()
	c := ts.client(t)
	resp, b := ts.do(t, c, "POST", "/api/setup", map[string]any{"username": "admin", "password": testPW})
	if resp.StatusCode != 201 {
		t.Fatalf("setup: %d %s", resp.StatusCode, b)
	}
	return c
}

// addUser creates a local user with a password directly in the store.
func (ts *testServer) addUser(t testing.TB, name, pw string, admin bool) store.User {
	t.Helper()
	h, err := auth.HashPassword(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := ts.Store.CreateUser(context.Background(), store.User{Username: name, PasswordHash: h, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// loginAs signs in and returns a client holding the session.
func (ts *testServer) loginAs(t testing.TB, name, pw string) *http.Client {
	t.Helper()
	c := ts.client(t)
	resp, b := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": name, "password": pw})
	if resp.StatusCode != 204 {
		t.Fatalf("login %s: %d %s", name, resp.StatusCode, b)
	}
	return c
}

func decode[T any](t testing.TB, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
	return v
}

func TestSetupFlow(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	c := ts.client(t)

	_, b := ts.do(t, c, "GET", "/api/setup/status", nil)
	if !decode[SetupStatus](t, b).NeedsSetup {
		t.Fatalf("status before setup: %s", b)
	}
	resp, b := ts.do(t, c, "POST", "/api/setup", map[string]any{"username": "admin", "password": testPW})
	if resp.StatusCode != 201 {
		t.Fatalf("setup: %d %s", resp.StatusCode, b)
	}
	if u := decode[UserOut](t, b); u.Username != "admin" || !u.IsAdmin || !u.IsBootstrap {
		t.Errorf("setup user = %+v", u)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("no session cookie on setup")
	}
	resp, b = ts.do(t, c, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("me: %d %s", resp.StatusCode, b)
	}
	me := decode[MeOut](t, b)
	if me.User.Username != "admin" || me.Via != "session" || strings.Join(me.Scopes, ",") != "read,control,admin" {
		t.Errorf("me = %+v", me)
	}
	_, b = ts.do(t, c, "GET", "/api/setup/status", nil)
	if decode[SetupStatus](t, b).NeedsSetup {
		t.Errorf("status after setup: %s", b)
	}
	resp, b = ts.do(t, ts.client(t), "POST", "/api/setup", map[string]any{"username": "other", "password": testPW})
	if resp.StatusCode != 409 || errCode(t, b) != "already_setup" {
		t.Errorf("second setup: %d %s", resp.StatusCode, b)
	}
}

func TestSetupValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"short password", map[string]any{"username": "admin", "password": "short"}, "password"},
		{"bad username", map[string]any{"username": "a b", "password": testPW}, "username"},
		{"bad public url", map[string]any{"username": "admin", "password": testPW, "public_url": "ftp://x"}, "public_url"},
	} {
		resp, b := ts.do(t, nil, "POST", "/api/setup", tc.body)
		if resp.StatusCode != 400 || !strings.Contains(string(b), tc.want) {
			t.Errorf("%s: %d %s, want 400 mentioning %q", tc.name, resp.StatusCode, b, tc.want)
		}
	}
	if n, _ := ts.Store.CountUsers(context.Background()); n != 0 {
		t.Errorf("invalid setup created %d users", n)
	}
}

func TestSetupSavesPublicURL(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	c := ts.client(t)
	resp, b := ts.do(t, c, "POST", "/api/setup", map[string]any{
		"username": "admin", "password": testPW, "public_url": "https://wattson.example.com"})
	if resp.StatusCode != 201 {
		t.Fatalf("setup: %d %s", resp.StatusCode, b)
	}
	s, err := ts.Settings.Get(context.Background())
	if err != nil || s.General.PublicURL != "https://wattson.example.com" {
		t.Errorf("public_url = %q, %v", s.General.PublicURL, err)
	}
	if !resp.Cookies()[0].Secure {
		t.Error("session cookie not Secure with an https public URL")
	}
}

func TestSetupConcurrent(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	const n = 8
	codes := make(chan int, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"username": fmt.Sprintf("admin%d", i), "password": testPW})
			resp, err := http.Post(ts.URL+"/api/setup", "application/json", bytes.NewReader(body))
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[201] != 1 || got[409] != n-1 {
		t.Errorf("status counts = %v, want one 201 and %d 409", got, n-1)
	}
	if c, _ := ts.Store.CountUsers(context.Background()); c != 1 {
		t.Errorf("users = %d, want 1", c)
	}
}
