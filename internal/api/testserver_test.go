package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/maptiles"
	"github.com/metril/wattson/internal/oidcauth/oidctest"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

// testServer is a full NewServer over httptest with its own database.
type testServer struct {
	URL      string
	Store    *store.Store
	Settings *settings.Service
	Box      *secretbox.Box
	Auth     *auth.Authenticator
	Log      *syncBuf // everything the server logged
	Bike     *ridectl.Manager
	Tiles    *maptiles.Manager // reads its directory and server from the settings
}

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type tsOpt func(*tsConfig)

type tsConfig struct {
	extra      []func(huma.API)
	riderInUse func(int64) bool
	routeInUse func(int64) bool
	openLocal  func() (blue.Transport, error)
	wsRecheck  time.Duration
	simDial    func(sim.Config) bike.Dial
	noBike     bool
	drawLimit  time.Duration
}

// withDrawTimeout shortens the BRouter draw call limit.
func withDrawTimeout(d time.Duration) tsOpt { return func(c *tsConfig) { c.drawLimit = d } }

// withSimDial injects the simulator dial the bike manager uses.
func withSimDial(f func(sim.Config) bike.Dial) tsOpt {
	return func(c *tsConfig) { c.simDial = f }
}

// withoutBike builds the server with no bike manager (Deps.Bike nil).
func withoutBike() tsOpt { return func(c *tsConfig) { c.noBike = true } }

// withOpenLocal injects the local BLE transport the bike manager opens.
func withOpenLocal(f func() (blue.Transport, error)) tsOpt {
	return func(c *tsConfig) { c.openLocal = f }
}

// withWSRecheck sets Deps.WSRecheck.
func withWSRecheck(d time.Duration) tsOpt {
	return func(c *tsConfig) { c.wsRecheck = d }
}

// withRiderInUse sets Deps.RiderInUse.
func withRiderInUse(f func(int64) bool) tsOpt {
	return func(c *tsConfig) { c.riderInUse = f }
}

// withRouteInUse sets Deps.RouteInUse.
func withRouteInUse(f func(int64) bool) tsOpt {
	return func(c *tsConfig) { c.routeInUse = f }
}

// withOps registers extra (test-only) operations on the server's huma API.
func withOps(fs ...func(huma.API)) tsOpt {
	return func(c *tsConfig) { c.extra = append(c.extra, fs...) }
}

func newTS(t testing.TB, opts ...tsOpt) *testServer {
	t.Helper()
	var cfg tsConfig
	for _, o := range opts {
		o(&cfg)
	}
	st := storetest.New(t)
	box, err := secretbox.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := settings.NewService(st, box)
	logs := &syncBuf{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	au := auth.NewAuthenticator(st, func(ctx context.Context) string {
		s, err := svc.Get(ctx)
		if err != nil {
			return ""
		}
		return s.General.PublicURL
	}, logger)
	mgr := ridectl.New(ridectl.Deps{Settings: svc.Get, LoadRoute: ridectl.StoreRoutes(st), OpenLocal: cfg.openLocal, SimDial: cfg.simDial, Logger: logger,
		TickEvery: 20 * time.Millisecond, SimPeriod: 20 * time.Millisecond})
	tiles := maptiles.New(svc.Get, http.DefaultClient)
	t.Cleanup(tiles.Close)
	d := Deps{Tiles: tiles, Store: st, Settings: svc, Box: box, Auth: au, HTTPClient: http.DefaultClient,
		Logger: logger, Version: "test", RiderInUse: cfg.riderInUse, RouteInUse: cfg.routeInUse, Bike: mgr, WSRecheck: cfg.wsRecheck, drawTimeout: cfg.drawLimit}
	if cfg.noBike {
		d.Bike = nil
	}
	h, err := newRouter(d, cfg.extra...)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { _ = mgr.Close(context.Background()) }) // runs first: ends WebSocket handlers
	return &testServer{URL: srv.URL, Store: st, Settings: svc, Box: box, Auth: au, Log: logs, Bike: mgr, Tiles: tiles}
}

// client returns an HTTP client with a cookie jar that does not follow redirects.
func (ts *testServer) client(t testing.TB) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// do sends method path with an optional JSON body and extra header
// key/value pairs, returning the response and its body.
func (ts *testServer) do(t testing.TB, c *http.Client, method, path string, body any, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if c == nil {
		c = ts.client(t)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

// mintToken creates an API token for a fresh user holding scopes and
// returns the bearer secret.
func (ts *testServer) mintToken(t testing.TB, username string, admin bool, scopes ...string) string {
	t.Helper()
	ctx := context.Background()
	u, err := ts.Store.CreateUser(ctx, store.User{Username: username, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	plain, prefix, hash := auth.GenerateToken()
	if _, err := ts.Store.CreateAPIToken(ctx, u.ID, "t", prefix, hash, scopes); err != nil {
		t.Fatal(err)
	}
	return plain
}

// oidcStart begins a login at slug, queues claims at the fake IdP and walks
// the redirects up to, not including, the callback. It returns the callback
// URL (on the test server) the IdP redirected to.
func (ts *testServer) oidcStart(t testing.TB, c *http.Client, idp *oidctest.IDP, slug, returnTo string, claims map[string]any) string {
	t.Helper()
	path := "/api/auth/oidc/" + slug + "/login"
	if returnTo != "" {
		path += "?return_to=" + url.QueryEscape(returnTo)
	}
	resp, b := ts.do(t, c, "GET", path, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login: %d %s", resp.StatusCode, b)
	}
	authURL := resp.Header.Get("Location")
	if !strings.HasPrefix(authURL, idp.URL+"/authorize") {
		t.Fatalf("login redirected to %q, want the IdP", authURL)
	}
	idp.Next(claims)
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cb := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(cb, ts.URL+"/api/auth/oidc/"+slug+"/callback") {
		t.Fatalf("IdP redirected %d to %q", resp.StatusCode, cb)
	}
	return cb
}

// oidcLogin follows login → IdP /authorize → callback and returns the final
// callback response (a redirect; its session cookie is in c's jar).
func (ts *testServer) oidcLogin(t testing.TB, c *http.Client, idp *oidctest.IDP, slug, returnTo string, claims map[string]any) *http.Response {
	t.Helper()
	cb := ts.oidcStart(t, c, idp, slug, returnTo, claims)
	resp, err := c.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}
