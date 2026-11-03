package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/settings"
)

// fakeBRouter answers /brouter with a fixed reply and records the queries.
type fakeBRouter struct {
	*httptest.Server
	calls atomic.Int32
	mu    sync.Mutex
	qs    []url.Values
}

const drawGeoJSON = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":{"type":"LineString",
"coordinates":[[7.5,45.5,300],[7.505,45.505,320],[7.51,45.51,340]]}}]}`

func newFakeBRouter(t testing.TB, status int, body string) *fakeBRouter {
	t.Helper()
	f := &fakeBRouter{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.mu.Lock()
		f.qs = append(f.qs, r.URL.Query())
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeBRouter) query(i int) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.qs) {
		return nil
	}
	return f.qs[i]
}

// closedURL returns the URL of a server that no longer listens.
func closedURL(t testing.TB) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

// segDir returns a temp directory holding the given (empty) tile files.
func segDir(t testing.TB, tiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range tiles {
		if err := os.WriteFile(filepath.Join(dir, n+".rd5"), []byte("rd5"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// indexServer serves a segments index listing tiles and returns its base URL.
func indexServer(t testing.TB, tiles ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, n := range tiles {
			fmt.Fprintf(w, `<a href="%s.rd5">%s.rd5</a>`+"\n", n, n)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

func (ts *testServer) setBRouter(t testing.TB, f func(*settings.BRouter)) {
	t.Helper()
	if _, err := ts.Settings.Update(t.Context(), func(s *settings.Settings) error { f(&s.BRouter); return nil }); err != nil {
		t.Fatal(err)
	}
}

type errEnvelope struct {
	Error struct {
		Code    string        `json:"code"`
		Message string        `json:"message"`
		Details *ErrorDetails `json:"details"`
	} `json:"error"`
}

func drawBody(extra map[string]any) map[string]any {
	b := map[string]any{"waypoints": [][2]float64{{7.5, 45.5}, {7.51, 45.51}}}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func TestDrawDryRun(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, segDir(t, "E5_N45") })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(map[string]any{"dry_run": true}))
	if resp.StatusCode != 200 {
		t.Fatalf("draw: %d %s", resp.StatusCode, b)
	}
	d := decode[DrawOut](t, b)
	if d.Route.ID != 0 || len(d.Profile.Lat) == 0 || d.Route.Source != "draw" || d.Route.Spec.Profile != "trekking" {
		t.Fatalf("draw out = %+v", d.Route)
	}
	if fb.query(0).Get("profile") != "trekking" || fb.calls.Load() != 1 {
		t.Errorf("brouter saw %v (%d calls)", fb.query(0), fb.calls.Load())
	}
	_, b = ts.do(t, admin, "GET", "/api/routes", nil)
	if string(b) != "[]\n" && string(b) != "[]" {
		t.Errorf("routes after dry run = %s", b)
	}
}

func TestDrawSave(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, segDir(t, "E5_N45") })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(map[string]any{"profile": "fastbike", "name": "Loop"}))
	if resp.StatusCode != 201 {
		t.Fatalf("draw: %d %s", resp.StatusCode, b)
	}
	d := decode[DrawOut](t, b)
	if d.Route.ID == 0 || d.Route.Source != "draw" || d.Route.Name != "Loop" || d.Profile.RouteID != d.Route.ID ||
		fb.query(0).Get("profile") != "fastbike" {
		t.Fatalf("draw out = %+v", d.Route)
	}
	resp, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/routes/%d", d.Route.ID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d %s", resp.StatusCode, b)
	}
	det := decode[RouteDetail](t, b)
	wp := det.Spec.Waypoints
	if len(wp) != 2 || wp[0] != [2]float64{7.5, 45.5} || wp[1] != [2]float64{7.51, 45.51} || det.Spec.Profile != "fastbike" {
		t.Errorf("spec = %+v", det.Spec)
	}
	// Default name.
	_, b = ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if n := decode[DrawOut](t, b).Route.Name; len(n) != len("Route 2006-01-02") || n[:6] != "Route " {
		t.Errorf("default name = %q", n)
	}
}

func TestDrawMissingTiles(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idx := indexServer(t, "E5_N45", "W5_N50")
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir, b.SegmentsURL = fb.URL, segDir(t), idx })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 422 || e.Error.Code != "missing_tiles" || !contains(e.Error.Message, "E5_N45") ||
		e.Error.Details == nil || len(e.Error.Details.MissingTiles) != 1 || e.Error.Details.MissingTiles[0] != "E5_N45" {
		t.Fatalf("missing: %d %s", resp.StatusCode, b)
	}
	if fb.calls.Load() != 0 {
		t.Errorf("brouter called %d times", fb.calls.Load())
	}
}

func TestDrawLookupMismatch(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 500, "lookup version mismatch (old rd5?) lookups.dat=10 W75_N40.rd5=11")
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL = fb.URL })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 502 || e.Error.Code != "brouter_outdated" || !contains(e.Error.Message, "newer format") ||
		(e.Error.Details != nil && len(e.Error.Details.MissingTiles) > 0) {
		t.Fatalf("mismatch: %d %s", resp.StatusCode, b)
	}
}

func TestDrawPublicFallback(t *testing.T) {
	t.Parallel()
	local := newFakeBRouter(t, 200, drawGeoJSON)
	public := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idx := indexServer(t, "E5_N45")
	ts.setBRouter(t, func(b *settings.BRouter) {
		b.URL, b.SegmentsDir, b.SegmentsURL, b.PublicURL, b.PublicFallback = local.URL, segDir(t), idx, public.URL, true
	})
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if resp.StatusCode != 201 {
		t.Fatalf("draw: %d %s", resp.StatusCode, b)
	}
	if local.calls.Load() != 0 || public.calls.Load() != 1 {
		t.Errorf("calls: local %d public %d", local.calls.Load(), public.calls.Load())
	}
}

func TestDrawPublicFallbackOffByDefault(t *testing.T) {
	t.Parallel()
	public := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idx := indexServer(t, "E5_N45")
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir, b.SegmentsURL, b.PublicURL = segDir(t), idx, public.URL })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if resp.StatusCode != 422 || public.calls.Load() != 0 {
		t.Fatalf("draw: %d %s (public calls %d)", resp.StatusCode, b, public.calls.Load())
	}
}

func TestDrawSkipsTileCheckWithoutDir(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, "/nonexistent-wattson" })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if resp.StatusCode != 201 {
		t.Fatalf("draw: %d %s", resp.StatusCode, b)
	}
}

func TestDrawRemoteBRouterWithoutSegmentsDir(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, "" })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if resp.StatusCode != 201 || fb.calls.Load() != 1 {
		t.Fatalf("draw: %d %s (%d calls)", resp.StatusCode, b, fb.calls.Load())
	}
}

// Tiles the segments server does not offer (open sea) do not block drawing.
func TestDrawIgnoresTilesNotOffered(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	idx := indexServer(t, "W5_N50")
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir, b.SegmentsURL = fb.URL, segDir(t), idx })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	if resp.StatusCode != 201 || fb.calls.Load() != 1 {
		t.Fatalf("draw: %d %s", resp.StatusCode, b)
	}
}

func TestDrawBRouterTimeout(t *testing.T) {
	t.Parallel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(slow.Close)
	ts := newTS(t, withDrawTimeout(100*time.Millisecond))
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = slow.URL, "" })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 502 || e.Error.Code != "brouter_unavailable" || !contains(e.Error.Message, "did not answer within 0.1 s") {
		t.Fatalf("timeout: %d %s", resp.StatusCode, b)
	}
}

func TestDrawBRouterDown(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	dead := closedURL(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = dead, segDir(t, "E5_N45") })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 502 || e.Error.Code != "brouter_unavailable" || e.Error.Message != "BRouter is not reachable at "+dead {
		t.Fatalf("down: %d %s", resp.StatusCode, b)
	}
}

func TestDrawBRouterError(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 500, "target island detected for section 1")
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, segDir(t, "E5_N45") })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 502 || e.Error.Code != "brouter_error" || !contains(e.Error.Message, "target island") {
		t.Fatalf("error: %d %s", resp.StatusCode, b)
	}
}

func TestDrawBRouterReportsMissing(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 500, "datafile E10_N45.rd5 not found")
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.URL, b.SegmentsDir = fb.URL, segDir(t, "E5_N45") })
	resp, b := ts.do(t, admin, "POST", "/api/routes/draw", drawBody(nil))
	e := decode[errEnvelope](t, b)
	if resp.StatusCode != 422 || e.Error.Code != "missing_tiles" || !contains(e.Error.Message, "E10_N45") ||
		e.Error.Details == nil || e.Error.Details.MissingTiles[0] != "E10_N45" {
		t.Fatalf("missing: %d %s", resp.StatusCode, b)
	}
}

func TestDrawValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	many := make([][2]float64, 31)
	for i := range many {
		many[i] = [2]float64{7.5 + float64(i)/1000, 45.5}
	}
	for name, body := range map[string]map[string]any{
		"one waypoint":   {"waypoints": [][2]float64{{7.5, 45.5}}},
		"31 waypoints":   {"waypoints": many},
		"lat/lon swap":   {"waypoints": [][2]float64{{45.5, 100}, {7.5, 45.5}}},
		"bad profile":    drawBody(map[string]any{"profile": "../x"}),
		"no waypoints":   {},
		"lon out of 180": {"waypoints": [][2]float64{{181, 10}, {7.5, 45.5}}},
	} {
		resp, b := ts.do(t, admin, "POST", "/api/routes/draw", body)
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
}

func TestDrawNeedsControl(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.setupAdmin(t)
	tok := "Bearer " + ts.mintToken(t, "viewer", false, "read")
	resp, _ := ts.do(t, nil, "POST", "/api/routes/draw", drawBody(nil), "Authorization", tok)
	if resp.StatusCode != 403 {
		t.Errorf("read token: %d", resp.StatusCode)
	}
}

func TestBRouterTestButton(t *testing.T) {
	t.Parallel()
	fb := newFakeBRouter(t, 200, drawGeoJSON)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir = segDir(t, "E5_N45") })
	post := func(body map[string]any) TestResult {
		t.Helper()
		resp, b := ts.do(t, admin, "POST", "/api/settings/test/brouter", body)
		if resp.StatusCode != 200 {
			t.Fatalf("test/brouter: %d %s", resp.StatusCode, b)
		}
		return decode[TestResult](t, b)
	}
	r := post(map[string]any{"url": fb.URL})
	if !r.OK || len(r.Message) < 14 || r.Message[:14] != "BRouter routed" || !contains(r.Message, "E5_N45") {
		t.Errorf("good = %+v", r)
	}
	// The route is 500 m long and centred in the tile.
	q := fb.query(0).Get("lonlats")
	if q != "7.500000,47.500000|7.500000,47.504492" {
		t.Errorf("lonlats = %q", q)
	}
	if r := post(map[string]any{"url": closedURL(t)}); r.OK || r.Message == "" {
		t.Errorf("closed = %+v", r)
	}
	// Without map regions any HTTP answer counts.
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir = segDir(t) })
	nf := newFakeBRouter(t, 400, "need lonlats")
	if r := post(map[string]any{"url": nf.URL}); !r.OK || r.Message != "BRouter reachable (no map regions downloaded)" {
		t.Errorf("no tiles = %+v", r)
	}
	if r := post(map[string]any{"url": closedURL(t)}); r.OK {
		t.Errorf("no tiles, closed = %+v", r)
	}
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir = "" })
	if r := post(map[string]any{"url": nf.URL}); !r.OK || r.Message != "BRouter reachable (no map regions downloaded)" {
		t.Errorf("empty segments dir = %+v", r)
	}
	if r := post(map[string]any{"url": "ftp://x"}); r.OK {
		t.Errorf("bad scheme = %+v", r)
	}
	tok := "Bearer " + ts.mintToken(t, "bob", false, "read", "control")
	if resp, _ := ts.do(t, nil, "POST", "/api/settings/test/brouter", map[string]any{}, "Authorization", tok); resp.StatusCode != 403 {
		t.Errorf("bob: %d", resp.StatusCode)
	}
}
