package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

var ctx = context.Background()

func boolp(b bool) *bool { return &b }

func mustUser(t *testing.T, s *store.Store, u store.User) store.User {
	t.Helper()
	got, err := s.CreateUser(ctx, u)
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", u.Username, err)
	}
	return got
}

func TestBootstrapOnce(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u, err := s.CreateBootstrapAdmin(ctx, "admin", "Admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsAdmin || !u.IsBootstrap || u.ID == 0 || u.PasswordHash != "hash" || u.CreatedAt.IsZero() {
		t.Fatalf("unexpected bootstrap user: %+v", u)
	}
	if _, err := s.CreateBootstrapAdmin(ctx, "admin2", "", "h"); !errors.Is(err, store.ErrAlreadySetup) {
		t.Fatalf("second call err = %v, want ErrAlreadySetup", err)
	}
}

func TestBootstrapAfterOtherUser(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "someone"})
	if _, err := s.CreateBootstrapAdmin(ctx, "admin", "", "h"); !errors.Is(err, store.ErrAlreadySetup) {
		t.Fatalf("err = %v, want ErrAlreadySetup", err)
	}
}

func TestBootstrapConcurrent(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.CreateBootstrapAdmin(ctx, "admin", "", "h")
		}()
	}
	wg.Wait()
	var ok, already int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrAlreadySetup):
			already++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || already != n-1 {
		t.Fatalf("ok=%d already=%d, want 1 and %d", ok, already, n-1)
	}
	if c, _ := s.CountUsers(ctx); c != 1 {
		t.Fatalf("CountUsers = %d, want 1", c)
	}
}

func TestUsernameCaseInsensitive(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "admin"})
	if _, err := s.CreateUser(ctx, store.User{Username: "Admin"}); !errors.Is(err, store.ErrNameConflict) {
		t.Fatalf("err = %v, want ErrNameConflict", err)
	}
	got, err := s.UserByUsername(ctx, "ADMIN")
	if err != nil || got.Username != "admin" {
		t.Fatalf("UserByUsername = %+v, %v", got, err)
	}
	if _, err := s.UserByUsername(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestIssSubUnique(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "a", Iss: "https://idp", Sub: "42"})
	if _, err := s.CreateUser(ctx, store.User{Username: "b", Iss: "https://idp", Sub: "42"}); !errors.Is(err, store.ErrNameConflict) {
		t.Fatalf("err = %v, want ErrNameConflict", err)
	}
	// Local users (no iss) never collide on the partial index.
	mustUser(t, s, store.User{Username: "c"})
	mustUser(t, s, store.User{Username: "d"})
	got, err := s.UserByIssSub(ctx, "https://idp", "42")
	if err != nil || got.Username != "a" {
		t.Fatalf("UserByIssSub = %+v, %v", got, err)
	}
	if _, err := s.UserByIssSub(ctx, "https://idp", "43"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUserByEmail(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	a := mustUser(t, s, store.User{Username: "a", Email: "Rider@Example.com"})
	mustUser(t, s, store.User{Username: "o", Email: "oidc@example.com", Iss: "https://idp", Sub: "1"})
	got, err := s.UserByEmail(ctx, "rider@example.COM")
	if err != nil || got.ID != a.ID || got.Email != "Rider@Example.com" {
		t.Fatalf("UserByEmail = %+v, %v", got, err)
	}
	if _, err := s.UserByEmail(ctx, "oidc@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oidc-only email: err = %v, want ErrNotFound", err)
	}
	if _, err := s.UserByEmail(ctx, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty email: err = %v, want ErrNotFound", err)
	}
	mustUser(t, s, store.User{Username: "b", Email: "RIDER@example.com"})
	if _, err := s.UserByEmail(ctx, "rider@example.com"); !errors.Is(err, store.ErrAmbiguous) {
		t.Fatalf("duplicate email: err = %v, want ErrAmbiguous", err)
	}
}

func TestUserLifecycle(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u := mustUser(t, s, store.User{Username: "u", DisplayName: "U", PasswordHash: "h1"})
	if err := s.SetPassword(ctx, u.ID, "h2"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkOIDC(ctx, u.ID, "https://idp", "s"); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserByID(ctx, u.ID)
	if err != nil || got.PasswordHash != "h2" || got.Iss != "https://idp" || got.Sub != "s" || got.DisplayName != "U" {
		t.Fatalf("UserByID = %+v, %v", got, err)
	}
	if _, err := s.UserByID(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetPassword(ctx, 999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetPassword missing: %v", err)
	}
	if err := s.LinkOIDC(ctx, 999, "i", "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("LinkOIDC missing: %v", err)
	}
	other := mustUser(t, s, store.User{Username: "v"})
	if err := s.LinkOIDC(ctx, other.ID, "https://idp", "s"); !errors.Is(err, store.ErrNameConflict) {
		t.Fatalf("LinkOIDC dup: %v", err)
	}
	list, err := s.ListUsers(ctx)
	if err != nil || len(list) != 2 || list[0].ID != u.ID {
		t.Fatalf("ListUsers = %+v, %v", list, err)
	}
	if n, err := s.CountUsers(ctx); err != nil || n != 2 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}
}

func TestSessionExpiry(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u := mustUser(t, s, store.User{Username: "u"})
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := s.CreateSession(ctx, "h", u.ID, t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionUser(ctx, "h", t0.Add(59*time.Minute))
	if err != nil || got.ID != u.ID {
		t.Fatalf("at 59m: %+v, %v", got, err)
	}
	if _, err := s.SessionUser(ctx, "h", t0.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("at 1h: err = %v, want ErrNotFound", err)
	}
	if _, err := s.SessionUser(ctx, "nope", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: err = %v", err)
	}
	// non-UTC input is normalised
	loc := time.FixedZone("x", 5*3600)
	if _, err := s.SessionUser(ctx, "h", t0.Add(30*time.Minute).In(loc)); err != nil {
		t.Fatalf("non-UTC now: %v", err)
	}
}

func TestSessionDisabledUser(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "admin", IsAdmin: true})
	u := mustUser(t, s, store.User{Username: "u"})
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := s.CreateSession(ctx, "h", u.ID, t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateUserFlags(ctx, u.ID, nil, boolp(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "h", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSessionDeletion(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u := mustUser(t, s, store.User{Username: "u"})
	v := mustUser(t, s, store.User{Username: "v"})
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		id  string
		uid int64
		exp time.Duration
	}{{"a", u.ID, time.Hour}, {"b", u.ID, time.Hour}, {"c", v.ID, time.Minute}, {"d", v.ID, time.Hour}} {
		if err := s.CreateSession(ctx, c.id, c.uid, t0, t0.Add(c.exp)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSession(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, "missing"); err != nil {
		t.Fatalf("deleting missing session: %v", err)
	}
	if _, err := s.SessionUser(ctx, "a", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session still valid: %v", err)
	}
	if n, err := s.DeleteExpiredSessions(ctx, t0.Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("DeleteExpiredSessions = %d, %v; want 1", n, err)
	}
	if n, err := s.DeleteUserSessions(ctx, u.ID); err != nil || n != 1 {
		t.Fatalf("DeleteUserSessions = %d, %v; want 1 (b)", n, err)
	}
	if _, err := s.SessionUser(ctx, "d", t0); err != nil {
		t.Fatalf("unrelated session removed: %v", err)
	}
}

func TestLastAdmin(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	b, err := s.CreateBootstrapAdmin(ctx, "boot", "", "h")
	if err != nil {
		t.Fatal(err)
	}
	a := mustUser(t, s, store.User{Username: "a", IsAdmin: true})
	if _, err := s.UpdateUserFlags(ctx, b.ID, boolp(false), nil); !errors.Is(err, store.ErrBootstrap) {
		t.Fatalf("demote bootstrap: %v", err)
	}
	if _, err := s.UpdateUserFlags(ctx, b.ID, nil, boolp(true)); !errors.Is(err, store.ErrBootstrap) {
		t.Fatalf("disable bootstrap: %v", err)
	}
	got, err := s.UpdateUserFlags(ctx, a.ID, boolp(false), nil)
	if err != nil || got.IsAdmin {
		t.Fatalf("demote A: %+v, %v", got, err)
	}
	if _, err := s.UpdateUserFlags(ctx, b.ID, boolp(false), nil); !errors.Is(err, store.ErrBootstrap) {
		t.Fatalf("demote lone bootstrap: %v", err)
	}
	// No-op flag changes on the bootstrap user are fine.
	if _, err := s.UpdateUserFlags(ctx, b.ID, boolp(true), boolp(false)); err != nil {
		t.Fatalf("no-op on bootstrap: %v", err)
	}
	if _, err := s.UpdateUserFlags(ctx, 999, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
}

func TestLastAdminNonBootstrap(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	if _, err := s.Write.Exec(`INSERT INTO users(username,is_admin,created_at) VALUES('solo',1,'2026-01-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername(ctx, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateUserFlags(ctx, u.ID, boolp(false), nil); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote: %v, want ErrLastAdmin", err)
	}
	if _, err := s.UpdateUserFlags(ctx, u.ID, nil, boolp(true)); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("disable: %v, want ErrLastAdmin", err)
	}
	// A disabled admin does not count as a remaining admin.
	other := mustUser(t, s, store.User{Username: "o", IsAdmin: true, Disabled: true})
	if _, err := s.UpdateUserFlags(ctx, u.ID, boolp(false), nil); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote with only a disabled peer: %v, want ErrLastAdmin", err)
	}
	if _, err := s.UpdateUserFlags(ctx, other.ID, nil, boolp(false)); err != nil {
		t.Fatalf("enable peer: %v", err)
	}
	if _, err := s.UpdateUserFlags(ctx, u.ID, boolp(false), nil); err != nil {
		t.Fatalf("demote with enabled peer: %v", err)
	}
}

func TestTokenOwnership(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u1 := mustUser(t, s, store.User{Username: "u1"})
	u2 := mustUser(t, s, store.User{Username: "u2"})
	tok, err := s.CreateAPIToken(ctx, u1.ID, "ci", "wat_abc", "hash1", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIToken(ctx, u2.ID, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign delete: %v, want ErrNotFound", err)
	}
	if l, _ := s.ListAPITokens(ctx, u1.ID); len(l) != 1 {
		t.Fatalf("token vanished: %v", l)
	}
	if err := s.DeleteAPIToken(ctx, u1.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIToken(ctx, u1.ID, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestTokenScopesRoundTrip(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	u := mustUser(t, s, store.User{Username: "u"})
	if _, err := s.CreateAPIToken(ctx, u.ID, "a", "p1", "h1", []string{"read", "control"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIToken(ctx, u.ID, "b", "p2", "h2", nil); err != nil {
		t.Fatal(err)
	}
	l, err := s.ListAPITokens(ctx, u.ID)
	if err != nil || len(l) != 2 {
		t.Fatalf("List = %v, %v", l, err)
	}
	if len(l[0].Scopes) != 2 || l[0].Scopes[0] != "read" || l[0].Scopes[1] != "control" {
		t.Fatalf("scopes = %v", l[0].Scopes)
	}
	if len(l[1].Scopes) != 0 || l[0].Name != "a" || l[0].Prefix != "p1" || l[0].LastUsedAt != nil || l[0].CreatedAt.IsZero() {
		t.Fatalf("unexpected tokens: %+v", l)
	}
	if _, err := s.CreateAPIToken(ctx, u.ID, "dup", "p3", "h1", nil); !errors.Is(err, store.ErrNameConflict) {
		t.Fatalf("duplicate hash: %v", err)
	}
}

func TestTokenUserAndTouch(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "admin", IsAdmin: true})
	u := mustUser(t, s, store.User{Username: "u"})
	tok, err := s.CreateAPIToken(ctx, u.ID, "ci", "p", "hh", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	gt, gu, err := s.TokenUser(ctx, "hh")
	if err != nil || gt.ID != tok.ID || gu.ID != u.ID {
		t.Fatalf("TokenUser = %+v %+v %v", gt, gu, err)
	}
	if _, _, err := s.TokenUser(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	if err := s.TouchAPIToken(ctx, tok.ID, at); err != nil {
		t.Fatal(err)
	}
	gt, _, _ = s.TokenUser(ctx, "hh")
	if gt.LastUsedAt == nil || !gt.LastUsedAt.Equal(at) {
		t.Fatalf("LastUsedAt = %v, want %v", gt.LastUsedAt, at)
	}
	if _, err := s.UpdateUserFlags(ctx, u.ID, nil, boolp(true)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TokenUser(ctx, "hh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled owner: %v, want ErrNotFound", err)
	}
}

func TestIssSubMustComeTogether(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	if _, err := s.CreateUser(ctx, store.User{Username: "a", Iss: "https://idp"}); !errors.Is(err, store.ErrIssSubMismatch) {
		t.Fatalf("iss without sub: %v", err)
	}
	if _, err := s.CreateUser(ctx, store.User{Username: "b", Sub: "abc"}); !errors.Is(err, store.ErrIssSubMismatch) {
		t.Fatalf("sub without iss: %v", err)
	}
	u := mustUser(t, s, store.User{Username: "c"})
	if err := s.LinkOIDC(ctx, u.ID, "https://idp", ""); !errors.Is(err, store.ErrIssSubMismatch) {
		t.Fatalf("LinkOIDC iss only: %v", err)
	}
	if err := s.LinkOIDC(ctx, u.ID, "", "abc"); !errors.Is(err, store.ErrIssSubMismatch) {
		t.Fatalf("LinkOIDC sub only: %v", err)
	}
	if err := s.LinkOIDC(ctx, u.ID, "https://idp", "abc"); err != nil {
		t.Fatalf("LinkOIDC both: %v", err)
	}
	if err := s.LinkOIDC(ctx, u.ID, "", ""); err != nil {
		t.Fatalf("LinkOIDC unlink: %v", err)
	}
	if _, err := s.CreateUser(ctx, store.User{Username: "d", Iss: "https://idp", Sub: "x"}); err != nil {
		t.Fatalf("both set: %v", err)
	}
}
