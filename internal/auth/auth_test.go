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
	"sync"
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
		"same host":           {"POST", "http://wattson.local:8080", true, 204},
		"same host upper":     {"DELETE", "http://WATTSON.local:8080", true, 204},
		"public url host":     {"PUT", publicURL, true, 204},
		"no origin":           {"POST", "", true, 204},
		"null origin":         {"POST", "null", true, 403},
		"other port":          {"POST", "http://wattson.local:9999", true, 403},
		"bearer foreign":      {"POST", "https://evil.example", false, 204},
		"cookie GET foreign":  {"GET", "https://evil.example", true, 204},
		"cookie OPTIONS":      {"OPTIONS", "https://evil.example", true, 204},
		"public default port": {"POST", "https://wattson.example:443", true, 403}, // public URL is :8443, not 443
	} {
		if got := do(tc.method, tc.origin, tc.cookie).Code; got != tc.want {
			t.Errorf("%s: code %d, want %d", name, got, tc.want)
		}
	}
}

func TestCSRFDefaultPorts(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := auth.NewAuthenticator(st, func(context.Context) string { return "https://wattson.example" },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	u := mkUser(t, st, "rider", false, false)
	c := mkSession(t, st, u.ID)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for name, tc := range map[string]struct {
		target, origin string
		want           int
	}{
		"https explicit 443 vs public": {"http://10.0.0.1/x", "https://wattson.example:443", 204},
		"https implicit vs public":     {"http://10.0.0.1/x", "https://wattson.example", 204},
		"http 80 vs host":              {"http://wattson.local/x", "http://wattson.local:80", 204},
		"host has :80, origin none":    {"http://wattson.local:80/x", "http://wattson.local", 204},
		"https 443 vs host":            {"https://wattson.local/x", "https://wattson.local:443", 204},
		"http :443 is not default":     {"http://wattson.local/x", "http://wattson.local:443", 403},
		"https :80 is not default":     {"http://wattson.local/x", "https://wattson.local:80", 403},
		"ipv6 default port":            {"http://[::1]/x", "http://[::1]:80", 204},
		"foreign still refused":        {"http://wattson.local/x", "https://evil.example:443", 403},
	} {
		r := httptest.NewRequest("POST", tc.target, nil)
		r.Header.Set("Origin", tc.origin)
		withCookie(r, c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: code %d, want %d", name, w.Code, tc.want)
		}
	}
}

func TestCSRFSecFetchSiteWithoutOrigin(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newAuth(t, st)
	u := mkUser(t, st, "rider", false, false)
	c := mkSession(t, st, u.ID)
	tok := mkToken(t, st, u.ID, "read", "control")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(method, site string, cookie bool) int {
		r := httptest.NewRequest(method, "http://wattson.local/x", nil)
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if cookie {
			withCookie(r, c)
		} else {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for name, tc := range map[string]struct {
		method, site string
		cookie       bool
		want         int
	}{
		"cross-site cookie POST": {"POST", "cross-site", true, 403},
		"same-origin":            {"POST", "same-origin", true, 204},
		"same-site":              {"POST", "same-site", true, 204},
		"none (navigation)":      {"POST", "none", true, 204},
		"absent":                 {"POST", "", true, 204},
		"cross-site GET":         {"GET", "cross-site", true, 204},
		"cross-site bearer":      {"POST", "cross-site", false, 204},
	} {
		if got := do(tc.method, tc.site, tc.cookie); got != tc.want {
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
		if res, _ := l.Allow("ip"); res == nil {
			t.Fatal("blocked after 9 failures")
		}
		l.Fail("ip")
		res, retry := l.Allow("ip")
		if res != nil || retry != 10*time.Minute {
			t.Fatalf("res=%v retry=%v", res, retry)
		}
		if res, _ := l.Allow("other"); res == nil {
			t.Fatal("keys not independent")
		}
		time.Sleep(4 * time.Minute)
		if res, retry := l.Allow("ip"); res != nil || retry != 6*time.Minute {
			t.Fatalf("retry after 4m = %v", retry)
		}
		time.Sleep(6 * time.Minute)
		if res, retry := l.Allow("ip"); res == nil || retry != 0 {
			t.Fatalf("after window: res=%v retry=%v", res, retry)
		}
		for i := 0; i < 10; i++ {
			l.Fail("ip")
		}
		l.Reset("ip")
		if res, _ := l.Allow("ip"); res == nil {
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
		res, retry := l.Allow("k")
		if res != nil || retry != 30*time.Second {
			t.Fatalf("res=%v retry=%v, want blocked for 30s (oldest leaves first)", res, retry)
		}
		time.Sleep(30 * time.Second)
		res, _ = l.Allow("k")
		if res == nil {
			t.Fatal("oldest failure should have left the window")
		}
		res.Release()
		// Many failures past max do not extend the lockout beyond the window.
		for i := 0; i < 1000; i++ {
			l.Fail("flood")
		}
		time.Sleep(time.Minute)
		res, _ = l.Allow("flood")
		if res == nil {
			t.Fatal("still blocked after window")
		}
		res.Release()
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

func TestLimiterConcurrentReservations(t *testing.T) {
	t.Parallel()
	const n, max = 50, 5
	l := auth.NewLimiter(max, time.Minute)
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		mu      sync.Mutex
		granted []*auth.Reservation
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if res, _ := l.Allow("ip"); res != nil {
				mu.Lock()
				granted = append(granted, res)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(granted) != max {
		t.Fatalf("%d concurrent attempts admitted, want %d", len(granted), max)
	}
	if res, _ := l.Allow("ip"); res != nil {
		t.Fatal("admitted while all slots in flight")
	}
	if res, _ := l.Allow("other"); res == nil {
		t.Fatal("other key affected")
	}
	// Release frees a slot; double-release does not free two.
	granted[0].Release()
	granted[0].Release()
	r1, _ := l.Allow("ip")
	if r1 == nil {
		t.Fatal("slot not freed by Release")
	}
	if r2, _ := l.Allow("ip"); r2 != nil {
		t.Fatal("double Release freed two slots")
	}
	// Fail converts in-flight to a recorded failure: still no free slot.
	granted[1].Fail()
	granted[1].Fail()
	if r, _ := l.Allow("ip"); r != nil {
		t.Fatal("Fail freed the slot")
	}
	// Success releases and clears recorded failures.
	granted[2].Success()
	granted[3].Release()
	granted[4].Release()
	r1.Release()
	if r, _ := l.Allow("ip"); r == nil {
		t.Fatal("Success did not clear failures")
	}
}

func TestLimiterFailedReservationsLockOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := auth.NewLimiter(3, time.Minute)
		for i := 0; i < 3; i++ {
			res, _ := l.Allow("k")
			if res == nil {
				t.Fatalf("attempt %d refused", i)
			}
			res.Fail()
		}
		res, retry := l.Allow("k")
		if res != nil || retry != time.Minute {
			t.Fatalf("res=%v retry=%v", res, retry)
		}
		time.Sleep(time.Minute)
		if res, _ := l.Allow("k"); res == nil {
			t.Fatal("still locked after window")
		}
	})
}

func TestTouchedPruned(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		st := storetest.New(t)
		a := newAuth(t, st)
		u := mkUser(t, st, "rider", false, false)
		toks := []string{mkToken(t, st, u.ID, "read"), mkToken(t, st, u.ID, "read"), mkToken(t, st, u.ID, "read")}
		use := func(tok string) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			if _, ok := a.Identify(r); !ok {
				t.Fatal("not identified")
			}
		}
		for _, tok := range toks {
			use(tok)
		}
		if n := a.TouchedLen(); n != 3 {
			t.Fatalf("touched = %d, want 3", n)
		}
		time.Sleep(2 * time.Minute)
		use(toks[0])
		if n := a.TouchedLen(); n != 1 {
			t.Fatalf("touched = %d after expiry sweep, want 1", n)
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
