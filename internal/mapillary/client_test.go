package mapillary

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
)

const testToken = "MLY|1234567|abcSECRET"

func newClient(srv *httptest.Server) *Client {
	return &Client{
		GraphURL: srv.URL, TilesURL: srv.URL + "/tiles", HTTP: srv.Client(),
		Token: func(context.Context) (string, error) { return testToken, nil },
		sleep: func(time.Duration) {},
	}
}

// tileBytes encodes an "image" layer for tile z/x/y from WGS84 points.
func tileBytes(t *testing.T, z, x, y int, pts []orb.Point, props []map[string]any) []byte {
	t.Helper()
	fc := geojson.NewFeatureCollection()
	for i, p := range pts {
		f := geojson.NewFeature(p)
		for k, v := range props[i] {
			f.Properties[k] = v
		}
		fc.Append(f)
	}
	layers := mvt.NewLayers(map[string]*geojson.FeatureCollection{"image": fc})
	layers.ProjectToTile(maptile.New(uint32(x), uint32(y), maptile.Zoom(z)))
	b, err := mvt.Marshal(layers)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCoverageDecodes(t *testing.T) {
	t.Parallel()
	const z, x, y = 14, 8508, 5843
	b := maptile.New(x, y, z).Bound()
	c := b.Center()
	pts := []orb.Point{{c[0] - 0.001, c[1] + 0.0005}, {c[0] + 0.002, c[1] - 0.001}}
	data := tileBytes(t, z, x, y, pts, []map[string]any{
		{"id": int64(111), "compass_angle": 90.0, "captured_at": int64(1700000000000), "is_pano": false},
		{"id": int64(222), "compass_angle": 270.5, "captured_at": int64(1600000000000), "is_pano": true},
	})
	var gotTok, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTok, gotPath = r.URL.Query().Get("access_token"), r.URL.Path
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	got, err := newClient(srv).Coverage(context.Background(), z, x, y)
	if err != nil {
		t.Fatal(err)
	}
	if gotTok != testToken || gotPath != fmt.Sprintf("/tiles/%d/%d/%d", z, x, y) {
		t.Errorf("request tok=%q path=%q", gotTok, gotPath)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	for i, want := range []Candidate{
		{ID: "111", Compass: 90, CapturedAt: 1700000000000},
		{ID: "222", Compass: 270.5, CapturedAt: 1600000000000, Pano: true},
	} {
		g := got[i]
		if g.ID != want.ID || g.Compass != want.Compass || g.CapturedAt != want.CapturedAt || g.Pano != want.Pano {
			t.Errorf("cand %d = %+v, want %+v", i, g, want)
		}
		if abs(g.Lon-pts[i][0]) > 1e-5 || abs(g.Lat-pts[i][1]) > 1e-5 {
			t.Errorf("cand %d at %f,%f want %f,%f", i, g.Lat, g.Lon, pts[i][1], pts[i][0])
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestCoverage404Empty(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	got, err := newClient(srv).Coverage(context.Background(), 14, 1, 1)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestNoToken(t *testing.T) {
	t.Parallel()
	c := &Client{GraphURL: "http://x", TilesURL: "http://x", Token: func(context.Context) (string, error) { return "", nil }}
	if _, err := c.Coverage(context.Background(), 14, 1, 1); !errors.Is(err, ErrNoToken) {
		t.Errorf("coverage err = %v", err)
	}
	if _, err := c.Image(context.Background(), "1"); !errors.Is(err, ErrNoToken) {
		t.Errorf("image err = %v", err)
	}
	if err := c.Ping(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Errorf("ping err = %v", err)
	}
}

func TestImageMeta(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "OAuth "+testToken || r.URL.Query().Get("access_token") != "" ||
			r.URL.Path != "/42" || r.URL.Query().Get("fields") != "thumb_2048_url,creator" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"thumb_2048_url":"%s/i/1.jpg","creator":{"username":"ana"}}`, srv.URL)
	}))
	t.Cleanup(srv.Close)
	got, err := newClient(srv).Image(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != srv.URL+"/i/1.jpg" || got.Creator != "ana" {
		t.Errorf("got %+v", got)
	}
}

func TestImageBadID(t *testing.T) {
	t.Parallel()
	c := &Client{GraphURL: "http://x", Token: func(context.Context) (string, error) { return "t", nil }}
	if _, err := c.Image(context.Background(), "../me?x=1"); err == nil {
		t.Fatal("want error for non-numeric id")
	}
}

func TestTokenRejected(t *testing.T) {
	t.Parallel()
	for _, code := range []int{401, 403} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		t.Cleanup(srv.Close)
		c := newClient(srv)
		if _, err := c.Coverage(context.Background(), 14, 1, 1); !errors.Is(err, ErrTokenRejected) || !strings.Contains(err.Error(), fmt.Sprint(code)) {
			t.Errorf("coverage %d err = %v", code, err)
		}
		if _, err := c.Image(context.Background(), "1"); !errors.Is(err, ErrTokenRejected) {
			t.Errorf("image %d err = %v", code, err)
		}
		if err := c.Ping(context.Background()); !errors.Is(err, ErrTokenRejected) {
			t.Errorf("ping %d err = %v", code, err)
		}
	}
	// Graph API reports an invalid token as 400 OAuthException.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token.","type":"OAuthException","code":190}}`))
	}))
	t.Cleanup(srv.Close)
	if err := newClient(srv).Ping(context.Background()); !errors.Is(err, ErrTokenRejected) {
		t.Errorf("400 oauth err = %v", err)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images" || r.Header.Get("Authorization") != "OAuth "+testToken ||
			r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("bbox") != "13.40,52.51,13.41,52.52" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	if err := newClient(srv).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitedRetries(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sleeps []time.Duration
	var codes []int
	var n int
	var retryAfterHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		code := codes[min(n, len(codes)-1)]
		n++
		ra := retryAfterHdr
		mu.Unlock()
		if ra != "" && code != 200 {
			w.Header().Set("Retry-After", ra)
		}
		w.WriteHeader(code)
		if code == 200 {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv)
	c.sleep = func(d time.Duration) { mu.Lock(); sleeps = append(sleeps, d); mu.Unlock() }

	codes = []int{429, 429, 200}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	mu.Lock()
	if len(sleeps) != 2 || sleeps[0] != time.Second || sleeps[1] != 2*time.Second {
		t.Errorf("sleeps = %v", sleeps)
	}
	mu.Unlock()

	mu.Lock()
	n, sleeps, codes = 0, nil, []int{429, 429, 429, 429}
	mu.Unlock()
	if err := c.Ping(context.Background()); !errors.Is(err, ErrRateLimited) {
		t.Errorf("4x429 err = %v", err)
	}
	mu.Lock()
	if n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
	mu.Unlock()

	// Retry-After (seconds) replaces the backoff and is capped at 10 s.
	mu.Lock()
	n, sleeps, codes, retryAfterHdr = 0, nil, []int{429, 503, 200}, "5"
	mu.Unlock()
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("retry-after: %v", err)
	}
	mu.Lock()
	if len(sleeps) != 2 || sleeps[0] != 5*time.Second || sleeps[1] != 5*time.Second {
		t.Errorf("retry-after sleeps = %v", sleeps)
	}
	n, sleeps, codes, retryAfterHdr = 0, nil, []int{429, 200}, "999"
	mu.Unlock()
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("retry-after cap: %v", err)
	}
	mu.Lock()
	if len(sleeps) != 1 || sleeps[0] != 10*time.Second {
		t.Errorf("capped sleeps = %v", sleeps)
	}
	retryAfterHdr = ""
	mu.Unlock()

	mu.Lock()
	n, codes = 0, []int{503, 200}
	mu.Unlock()
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("503 then 200: %v", err)
	}
}

func TestErrorRedactsToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // connection refused; the transport error carries the URL
	c := newClient(srv)
	_, err := c.Coverage(context.Background(), 14, 1, 1)
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "access_token=REDACTED") {
		t.Errorf("error = %q", err)
	}
	// 5xx and unexpected statuses also carry only the redacted URL.
	srv5 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(srv5.Close)
	_, err = newClient(srv5).Coverage(context.Background(), 14, 1, 1)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("5xx error = %v", err)
	}
}

func TestBadImageHost(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"thumb_2048_url":"http://evil.example/i.jpg","creator":{"username":"x"}}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := newClient(srv).Image(context.Background(), "1"); !errors.Is(err, ErrBadImageHost) {
		t.Errorf("err = %v", err)
	}
}

func TestDefaultImageHosts(t *testing.T) {
	t.Parallel()
	c := &Client{GraphURL: DefaultGraphURL}
	for u, want := range map[string]bool{
		"https://scontent-ams.xx.fbcdn.net/m1/x.jpg": true,
		"https://images.mapillary.com/x.jpg":         true,
		"https://graph.mapillary.com/x":              true,
		"https://evilfbcdn.net/x":                    false,
		"https://fbcdn.net.evil.example/x":           false,
		"ftp://scontent.fbcdn.net/x":                 false,
	} {
		if got := c.imageHostOK(context.Background(), u); got != want {
			t.Errorf("%s = %v, want %v", u, got, want)
		}
	}
}

func TestScrubForms(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"GET https://x/14/1/1?access_token=" + url.QueryEscape(testToken) + ": boom",
		"raw " + testToken + " raw",
		"path /" + url.PathEscape(testToken) + "/x",
	} {
		if out := scrub(in, testToken); strings.Contains(out, "1234567") || !strings.Contains(out, redactedVal) {
			t.Errorf("scrub(%q) = %q", in, out)
		}
	}
}

func TestTilesURLWithQuery(t *testing.T) {
	t.Parallel()
	var gotQ url.Values
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ, gotPath = r.URL.Query(), r.URL.Path
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv)
	c.TilesURL = srv.URL + "/tiles/?x=1"
	if _, err := c.Coverage(context.Background(), 14, 5, 6); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/tiles/14/5/6" || gotQ.Get("x") != "1" || gotQ.Get("access_token") != testToken {
		t.Errorf("path %q query %v", gotPath, gotQ)
	}
}
