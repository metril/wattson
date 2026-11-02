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
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
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
}

type tsOpt func(*tsConfig)

type tsConfig struct {
	extra []func(huma.API)
}

// withOps registers extra (test-only) operations on the server's huma API.
func withOps(f func(huma.API)) tsOpt {
	return func(c *tsConfig) { c.extra = append(c.extra, f) }
}

func newTS(t *testing.T, opts ...tsOpt) *testServer {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	au := auth.NewAuthenticator(st, func(ctx context.Context) string {
		s, err := svc.Get(ctx)
		if err != nil {
			return ""
		}
		return s.General.PublicURL
	}, logger)
	d := Deps{Store: st, Settings: svc, Box: box, Auth: au, HTTPClient: http.DefaultClient,
		Logger: logger, Version: "test"}
	srv := httptest.NewServer(newRouter(d, cfg.extra...))
	t.Cleanup(srv.Close)
	return &testServer{URL: srv.URL, Store: st, Settings: svc, Box: box, Auth: au}
}

// client returns an HTTP client with a cookie jar that does not follow redirects.
func (ts *testServer) client(t *testing.T) *http.Client {
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
func (ts *testServer) do(t *testing.T, c *http.Client, method, path string, body any, hdr ...string) (*http.Response, []byte) {
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
func (ts *testServer) mintToken(t *testing.T, username string, admin bool, scopes ...string) string {
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
