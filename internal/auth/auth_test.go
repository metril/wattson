package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

var ctx = context.Background()

const publicURL = "https://wattson.example:8443"

func newAuth(t *testing.T, st *store.Store) *auth.Authenticator {
	t.Helper()
	return auth.NewAuthenticator(st, func(context.Context) string { return publicURL },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func mkUser(t *testing.T, st *store.Store, name string, admin, disabled bool) store.User {
	t.Helper()
	u, err := st.CreateUser(ctx, store.User{Username: name, IsAdmin: admin, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mkSession(t *testing.T, st *store.Store, uid int64) (cookie string) {
	t.Helper()
	plain, hash := auth.GenerateSecret()
	now := time.Now()
	if err := st.CreateSession(ctx, hash, uid, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return plain
}

func mkToken(t *testing.T, st *store.Store, uid int64, scopes ...string) string {
	t.Helper()
	plain, prefix, hash := auth.GenerateToken()
	if _, err := st.CreateAPIToken(ctx, uid, "t", prefix, hash, scopes); err != nil {
		t.Fatal(err)
	}
	return plain
}

func withCookie(r *http.Request, v string) *http.Request {
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: v})
	return r
}

func scopesEq(got []auth.Scope, want ...auth.Scope) bool { return reflect.DeepEqual(got, want) }

func TestSessionIdentity(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "root", true, false)
	c := mkSession(t, st, u.ID)
	id, ok := a.Identify(withCookie(httptest.NewRequest("GET", "/", nil), c))
	if !ok || id.Via != "session" || id.User.ID != u.ID || id.SessionHash != auth.HashSecret(c) {
		t.Fatalf("id=%+v ok=%v", id, ok)
	}
	if !scopesEq(id.Scopes, auth.ScopeRead, auth.ScopeControl, auth.ScopeAdmin) {
		t.Fatalf("scopes = %v", id.Scopes)
	}
	// Non-admin gets read+control only.
	u2 := mkUser(t, st, "rider", false, false)
	id, ok = a.Identify(withCookie(httptest.NewRequest("GET", "/", nil), mkSession(t, st, u2.ID)))
	if !ok || !scopesEq(id.Scopes, auth.ScopeRead, auth.ScopeControl) {
		t.Fatalf("rider id=%+v ok=%v", id, ok)
	}
}

func TestTokenIdentity(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "rider", false, false)
	tok := mkToken(t, st, u.ID, "read")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	id, ok := a.Identify(r)
	if !ok || id.Via != "token" || id.TokenID == 0 || id.User.ID != u.ID {
		t.Fatalf("id=%+v ok=%v", id, ok)
	}
	if !id.Has(auth.ScopeRead) || id.Has(auth.ScopeControl) || id.Has(auth.ScopeAdmin) {
		t.Fatalf("scopes = %v", id.Scopes)
	}
}

func TestTokenAdminScopeDroppedForNonAdmin(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "demoted", false, false)
	tok := mkToken(t, st, u.ID, "read", "admin")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	id, ok := a.Identify(r)
	if !ok || !scopesEq(id.Scopes, auth.ScopeRead) {
		t.Fatalf("id=%+v ok=%v", id, ok)
	}
	// An admin-only token of a non-admin has nothing left: anonymous.
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+mkToken(t, st, u.ID, "admin"))
	if _, ok := a.Identify(r); ok {
		t.Fatal("admin-only token of non-admin identified")
	}
	// A real admin keeps it.
	adm := mkUser(t, st, "adm", true, false)
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+mkToken(t, st, adm.ID, "read", "admin"))
	if id, ok := a.Identify(r); !ok || !scopesEq(id.Scopes, auth.ScopeRead, auth.ScopeAdmin) {
		t.Fatalf("admin token id=%+v ok=%v", id, ok)
	}
}

func TestDisabledUserAnonymous(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "off", false, false)
	c := mkSession(t, st, u.ID)
	tok := mkToken(t, st, u.ID, "read")
	off := true
	if _, err := st.UpdateUserFlags(ctx, u.ID, nil, &off); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Identify(withCookie(httptest.NewRequest("GET", "/", nil), c)); ok {
		t.Fatal("disabled user's session identified")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if _, ok := a.Identify(r); ok {
		t.Fatal("disabled user's token identified")
	}
}

func TestAdminImpliesAll(t *testing.T) {
	t.Parallel()
	id := auth.Identity{Scopes: []auth.Scope{auth.ScopeAdmin}}
	if !id.Has(auth.ScopeControl) || !id.Has(auth.ScopeRead) || !id.Has(auth.ScopeAdmin) {
		t.Fatal("admin must satisfy every scope")
	}
	if (auth.Identity{Scopes: []auth.Scope{auth.ScopeControl}}).Has(auth.ScopeRead) {
		t.Fatal("control must not imply read")
	}
}

func TestWSProtocolToken(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "rider", false, false)
	tok := mkToken(t, st, u.ID, "read", "control")
	for _, hdr := range [][]string{
		{"wattson.v1, bearer." + tok},
		{"wattson.v1", "bearer." + tok},
	} {
		r := httptest.NewRequest("GET", "/ws", nil)
		r.Header["Sec-Websocket-Protocol"] = hdr
		id, ok := a.Identify(r)
		if !ok || id.Via != "token" || !id.Has(auth.ScopeControl) {
			t.Fatalf("hdr %q: id=%+v ok=%v", hdr, id, ok)
		}
	}
}

func TestBadTokenAnonymous(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "rider", false, false)
	c := mkSession(t, st, u.ID)
	for name, mod := range map[string]func(*http.Request){
		"bad bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer wat_nope") },
		"bad cookie":  func(r *http.Request) { withCookie(r, "garbage") },
		"basic auth":  func(r *http.Request) { r.Header.Set("Authorization", "Basic Zm9vOmJhcg==") },
		"bad ws":      func(r *http.Request) { r.Header.Set("Sec-WebSocket-Protocol", "wattson.v1, bearer.wat_nope") },
		"bad+session": func(r *http.Request) { r.Header.Set("Authorization", "Bearer wat_nope"); withCookie(r, c) },
	} {
		called := false
		h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if _, ok := auth.FromContext(r.Context()); ok {
				t.Errorf("%s: identity attached", name)
			}
		}))
		r := httptest.NewRequest("GET", "/", nil)
		mod(r)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !called {
			t.Errorf("%s: next handler did not run", name)
		}
	}
}

func TestCookieMutationForeignOrigin403(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "rider", false, false)
	c := mkSession(t, st, u.ID)
	tok := mkToken(t, st, u.ID, "read", "control")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	do := func(method, origin string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://wattson.local:8080/api/x", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie {
			withCookie(r, c)
		} else {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := do("POST", "https://evil.example", true)
	if w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "csrf" ||
		body.Error.Message != "cross-origin request refused" {
		t.Fatalf("body = %s (%v)", w.Body.String(), err)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	for name, tc := range map[string]struct {
		method, origin string
		cookie         bool
		want           int
	}{
		"same host":          {"POST", "http://wattson.local:8080", true, 204},
		"same host upper":    {"DELETE", "http://WATTSON.local:8080", true, 204},
		"public url host":    {"PUT", publicURL, true, 204},
		"no origin":          {"POST", "", true, 204},
		"null origin":        {"POST", "null", true, 403},
		"other port":         {"POST", "http://wattson.local:9999", true, 403},
		"bearer foreign":     {"POST", "https://evil.example", false, 204},
		"cookie GET foreign": {"GET", "https://evil.example", true, 204},
		"cookie OPTIONS":     {"OPTIONS", "https://evil.example", true, 204},
	} {
		if got := do(tc.method, tc.origin, tc.cookie).Code; got != tc.want {
			t.Errorf("%s: code %d, want %d", name, got, tc.want)
		}
	}
}

func TestParseScopes(t *testing.T) {
	t.Parallel()
	if _, err := auth.ParseScopes(nil); err == nil {
		t.Fatal("empty accepted")
	}
	if _, err := auth.ParseScopes([]string{"write"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	got, err := auth.ParseScopes([]string{"admin", "read", "read"})
	if err != nil || !scopesEq(got, auth.ScopeRead, auth.ScopeAdmin) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
	p, h := auth.GenerateSecret()
	p2, _ := auth.GenerateSecret()
	if len(p) != 43 || p == p2 || h != auth.HashSecret(p) || len(h) != 64 {
		t.Fatalf("secret %q hash %q", p, h)
	}
	if strings.Contains(h, p) {
		t.Fatal("hash contains plaintext")
	}
	tp, prefix, th := auth.GenerateToken()
	if len(tp) != 47 || !strings.HasPrefix(tp, "wat_") || prefix != tp[:12] || th != auth.HashSecret(tp) {
		t.Fatalf("token %q prefix %q", tp, prefix)
	}
	if auth.HashSecret("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("HashSecret is not hex sha256")
	}
}

func TestLimiter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := auth.NewLimiter(10, 10*time.Minute)
		for i := 0; i < 9; i++ {
			l.Fail("ip")
		}
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatal("blocked after 9 failures")
		}
		l.Fail("ip")
		ok, retry := l.Allow("ip")
		if ok || retry != 10*time.Minute {
			t.Fatalf("ok=%v retry=%v", ok, retry)
		}
		if ok, _ := l.Allow("other"); !ok {
			t.Fatal("keys not independent")
		}
		time.Sleep(4 * time.Minute)
		if _, retry := l.Allow("ip"); retry != 6*time.Minute {
			t.Fatalf("retry after 4m = %v", retry)
		}
		time.Sleep(6 * time.Minute)
		if ok, retry := l.Allow("ip"); !ok || retry != 0 {
			t.Fatalf("after window: ok=%v retry=%v", ok, retry)
		}
		for i := 0; i < 10; i++ {
			l.Fail("ip")
		}
		l.Reset("ip")
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatal("Reset did not clear")
		}
	})
}

func TestLimiterSlidingAndBounded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := auth.NewLimiter(3, time.Minute)
		l.Fail("k")
		time.Sleep(30 * time.Second)
		l.Fail("k")
		l.Fail("k")
		ok, retry := l.Allow("k")
		if ok || retry != 30*time.Second {
			t.Fatalf("ok=%v retry=%v, want blocked for 30s (oldest leaves first)", ok, retry)
		}
		time.Sleep(30 * time.Second)
		if ok, _ := l.Allow("k"); !ok {
			t.Fatal("oldest failure should have left the window")
		}
		// Many failures past max do not extend the lockout beyond the window.
		for i := 0; i < 1000; i++ {
			l.Fail("flood")
		}
		time.Sleep(time.Minute)
		if ok, _ := l.Allow("flood"); !ok {
			t.Fatal("still blocked after window")
		}
		// Expired keys are swept so the map cannot grow without bound.
		for i := 0; i < 20000; i++ {
			l.Fail(string(rune('a'+i%26)) + time.Duration(i).String())
		}
		time.Sleep(2 * time.Minute)
		l.Fail("trigger")
		if n := l.Size(); n > 2 {
			t.Fatalf("limiter retains %d keys after expiry", n)
		}
	})
}

func TestTokenTouchThrottled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		st := storetest.New(t)
		a := newAuth(t, st)
		u := mkUser(t, st, "rider", false, false)
		tok := mkToken(t, st, u.ID, "read")
		req := func() {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			if _, ok := a.Identify(r); !ok {
				t.Fatal("token not identified")
			}
		}
		lastUsed := func() time.Time {
			ts, err := st.ListAPITokens(ctx, u.ID)
			if err != nil || len(ts) != 1 || ts[0].LastUsedAt == nil {
				t.Fatalf("tokens=%+v err=%v", ts, err)
			}
			return *ts[0].LastUsedAt
		}
		t0 := time.Now()
		req()
		if got := lastUsed(); !got.Equal(t0) {
			t.Fatalf("first use wrote %v, want %v", got, t0)
		}
		time.Sleep(time.Second)
		req()
		if got := lastUsed(); !got.Equal(t0) {
			t.Fatalf("second use within 60s wrote %v", got)
		}
		time.Sleep(60 * time.Second) // now t0+61s
		req()
		if got := lastUsed(); !got.Equal(t0.Add(61 * time.Second)) {
			t.Fatalf("use at +61s wrote %v, want %v", got, t0.Add(61*time.Second))
		}
	})
}
