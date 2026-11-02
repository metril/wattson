package api

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/mapillary"
	"github.com/metril/wattson/internal/mapillary/mapillarytest"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const mlyToken = "MLY|7777|SECRETabc123"

// rampImages places three photos facing north along ramp.gpx (45N 7E, due north).
func rampImages() []mapillarytest.Image {
	var out []mapillarytest.Image
	for i, lat := range []float64{45.0060, 45.0003, 45.0030} {
		out = append(out, mapillarytest.Image{ID: int64(5000 + i), Lat: lat, Lon: 7.00002, Compass: 2,
			CapturedAt: 1700000000000, Creator: "ana"})
	}
	return out
}

func (ts *testServer) setMapillaryToken(t testing.TB, tok string) {
	t.Helper()
	_, err := ts.Settings.Update(context.Background(), func(s *settings.Settings) error {
		s.Mapillary.Token = tok
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (ts *testServer) importRamp(t testing.TB, admin *http.Client) int64 {
	t.Helper()
	resp, b := ts.importFile(t, admin, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 201 {
		t.Fatalf("import: %d %s", resp.StatusCode, b)
	}
	return decode[RouteDetail](t, b).ID
}

// buildRamp imports ramp.gpx, sets the token and waits for the photo list, so
// the fake's images 5000 to 5002 are listed by a route.
func (ts *testServer) buildRamp(t testing.TB, admin *http.Client) int64 {
	t.Helper()
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	if out := ts.pollPhotos(t, admin, id); out.Status != "ready" || len(out.Photos) != 3 {
		t.Fatalf("build: %+v", out)
	}
	return id
}

func (ts *testServer) photos(t testing.TB, c *http.Client, id int64) (*http.Response, RoutePhotosOut, []byte) {
	t.Helper()
	resp, b := ts.do(t, c, "GET", "/api/routes/"+itoa(id)+"/mapillary", nil)
	var out RoutePhotosOut
	if resp.StatusCode == 200 {
		out = decode[RoutePhotosOut](t, b)
	}
	return resp, out, b
}

// pollPhotos asks until the state leaves building (2 s deadline).
func (ts *testServer) pollPhotos(t testing.TB, c *http.Client, id int64) RoutePhotosOut {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, out, b := ts.photos(t, c, id)
		if resp.StatusCode != 200 {
			t.Fatalf("photos: %d %s", resp.StatusCode, b)
		}
		if out.Status != "building" {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatal("still building after 2 s")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestPhotosDisabledWithoutToken(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	resp, out, b := ts.photos(t, admin, id)
	if resp.StatusCode != 200 || out.Status != "disabled" || out.Photos == nil || !strings.Contains(string(b), `"photos":[]`) {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
}

func TestPhotosUnknownRouteAndAuth(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "GET", "/api/routes/9999/mapillary", nil); resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("unknown route: %d %s", resp.StatusCode, b)
	}
	if resp, _ := ts.do(t, nil, "GET", "/api/routes/1/mapillary", nil); resp.StatusCode != 401 {
		t.Errorf("anonymous: %d", resp.StatusCode)
	}
}

func TestPhotosBuildThenReady(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	f.SetToken(mlyToken)
	if _, out, _ := ts.photos(t, admin, id); out.Status != "building" {
		t.Fatalf("first status = %q, want building", out.Status)
	}
	out := ts.pollPhotos(t, admin, id)
	if out.Status != "ready" || len(out.Photos) < 1 || out.BuiltAt == nil {
		t.Fatalf("ready = %+v", out)
	}
	for i := 1; i < len(out.Photos); i++ {
		if out.Photos[i].DM < out.Photos[i-1].DM {
			t.Errorf("not sorted by d_m: %+v", out.Photos)
		}
	}
	n := f.TileRequests()
	if _, again, _ := ts.photos(t, admin, id); again.Status != "ready" || f.TileRequests() != n {
		t.Errorf("second get: %q, tile requests %d -> %d", again.Status, n, f.TileRequests())
	}
}

func TestPhotosRebuildOnTokenChange(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	ts.pollPhotos(t, admin, id)
	ts.setMapillaryToken(t, mlyToken+"-new")
	if _, out, _ := ts.photos(t, admin, id); out.Status != "building" {
		t.Fatalf("after token change: %q, want building", out.Status)
	}
	if out := ts.pollPhotos(t, admin, id); out.Status != "ready" {
		t.Errorf("after rebuild: %q", out.Status)
	}
}

func TestBuildTokenRejected(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	f.SetTileStatus(http.StatusUnauthorized)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	out := ts.pollPhotos(t, admin, id)
	if out.Status != "error" || out.Message != "Mapillary rejected the token. Check it in Settings → Integrations." {
		t.Fatalf("got %+v", out)
	}
}

func TestBuildNoCoverage(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t) // every tile 404
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	resp, out, b := ts.photos(t, admin, id)
	if out.Status == "building" {
		out = ts.pollPhotos(t, admin, id)
		resp, _, b = ts.photos(t, admin, id)
	}
	if resp.StatusCode != 200 || out.Status != "ready" || !strings.Contains(string(b), `"photos":[]`) {
		t.Fatalf("got %d %s", resp.StatusCode, b)
	}
}

func TestPhotosSyntheticUnavailable(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r, err := ts.Store.CreateRoute(context.Background(), store.Route{Name: "s", Source: "synthetic", GeoJSON: []byte(`{}`), Points: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	ts.setMapillaryToken(t, mlyToken)
	_, out, _ := ts.photos(t, admin, r.ID)
	if out.Status != "unavailable" || out.Message != "Synthetic routes have no street photos" {
		t.Errorf("got %+v", out)
	}
}

func TestRoutePhotosCascade(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	ts.pollPhotos(t, admin, id)
	if _, err := ts.Store.GetRoutePhotos(context.Background(), id); err != nil {
		t.Fatalf("row missing before delete: %v", err)
	}
	if resp, b := ts.do(t, admin, "DELETE", "/api/routes/"+itoa(id), nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if _, err := ts.Store.GetRoutePhotos(context.Background(), id); err != store.ErrNotFound {
		t.Errorf("row after delete: %v, want ErrNotFound", err)
	}
}

func TestImageProxy(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	f.SetToken(mlyToken)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	ts.buildRamp(t, admin)
	resp, b := ts.do(t, admin, "GET", "/api/mapillary/images/5000", nil)
	if resp.StatusCode != 200 || !bytes.Equal(b, mapillarytest.JPEG()) {
		t.Fatalf("image: %d (%d bytes)", resp.StatusCode, len(b))
	}
	for k, want := range map[string]string{"Content-Type": "image/jpeg", "X-Photo-Creator": "ana",
		"Cache-Control": "private, max-age=86400", "X-Content-Type-Options": "nosniff"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, bad := range []string{"..%2F1", "abc", "1;2", "123456789012345678901", "5000.jpg"} {
		resp, b := ts.do(t, admin, "GET", "/api/mapillary/images/"+bad, nil)
		if resp.StatusCode != 400 || errCode(t, b) != "bad_id" {
			t.Errorf("id %q: %d %s, want 400 bad_id", bad, resp.StatusCode, b)
		}
	}
	graph := f.GraphRequests()
	if resp, b := ts.do(t, admin, "GET", "/api/mapillary/images/424242", nil); resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("unlisted image: %d %s", resp.StatusCode, b)
	}
	if f.GraphRequests() != graph {
		t.Error("an unlisted id reached Mapillary")
	}
	if resp, _ := ts.do(t, nil, "GET", "/api/mapillary/images/5000", nil); resp.StatusCode != 401 {
		t.Errorf("anonymous: %d", resp.StatusCode)
	}
	kiosk := ts.mintToken(t, "kiosk", false, "read")
	if resp, _ := ts.do(t, nil, "GET", "/api/mapillary/images/5000", nil, "Authorization", "Bearer "+kiosk); resp.StatusCode != 200 {
		t.Errorf("read token: %d", resp.StatusCode)
	}
}

func TestImageProxyUpstreamFailures(t *testing.T) {
	t.Parallel()
	big := append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, mapillary.MaxThumbBytes)...)
	for _, tc := range []struct {
		name   string
		status int
		ctype  string
		body   []byte
	}{
		{"500", 500, "image/jpeg", mapillarytest.JPEG()},
		{"html", 200, "text/html", []byte("<script>alert(1)</script>")},
		{"svg", 200, "image/svg+xml", []byte("<svg/>")},
		{"oversize", 200, "image/jpeg", big},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := mapillarytest.New(t, rampImages()...)
			f.SetThumb(tc.status, tc.ctype, tc.body)
			ts := newTS(t, withMapillary(f))
			admin := ts.setupAdmin(t)
			ts.buildRamp(t, admin)
			resp, b := ts.do(t, admin, "GET", "/api/mapillary/images/5000", nil)
			if resp.StatusCode != 502 || errCode(t, b) != "photo_unavailable" || strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
				t.Errorf("%d %s (%s)", resp.StatusCode, b, resp.Header.Get("Content-Type"))
			}
		})
	}
}

func TestTokenNeverExposed(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	f.SetToken(mlyToken)
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	var seen []string
	grab := func(resp *http.Response, b []byte) {
		seen = append(seen, string(b))
		for k, vs := range resp.Header {
			seen = append(seen, k+": "+strings.Join(vs, ","))
		}
	}
	grab(ts.do(t, admin, "GET", "/api/routes/"+itoa(id)+"/mapillary", nil)) // building
	ts.pollPhotos(t, admin, id)
	grab(ts.do(t, admin, "GET", "/api/routes/"+itoa(id)+"/mapillary", nil))
	grab(ts.do(t, admin, "GET", "/api/mapillary/images/5000", nil))
	grab(ts.do(t, admin, "GET", "/api/mapillary/images/424242", nil))
	grab(ts.do(t, admin, "GET", "/api/mapillary/images/nope", nil))
	grab(ts.do(t, admin, "GET", "/api/settings", nil))
	grab(ts.do(t, admin, "POST", "/api/settings/test/mapillary", map[string]any{}))
	// A rejected token and an unreachable upstream must not leak it either.
	f.SetTileStatus(http.StatusUnauthorized)
	ts.setMapillaryToken(t, mlyToken+"x")
	ts.photos(t, admin, id)
	ts.pollPhotos(t, admin, id)
	grab(ts.do(t, admin, "GET", "/api/routes/"+itoa(id)+"/mapillary", nil))
	seen = append(seen, ts.Log.String())
	all := strings.Join(seen, "\n")
	for _, form := range []string{mlyToken, url.QueryEscape(mlyToken), url.PathEscape(mlyToken), "SECRETabc123"} {
		if strings.Contains(all, form) {
			t.Errorf("the token (%q) appears in a response, header or log", form)
		}
	}
}

func TestDeleteRouteCancelsBuild(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	release := f.GateTiles()
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	for f.TileRequests() == 0 {
		time.Sleep(time.Millisecond)
	}
	if resp, b := ts.do(t, admin, "DELETE", "/api/routes/"+itoa(id), nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	// The gated tile requests were cancelled with the build: the fake sees them end.
	release()
	if resp, _ := ts.do(t, admin, "GET", "/api/routes/"+itoa(id)+"/mapillary", nil); resp.StatusCode != 404 {
		t.Errorf("after delete: %d", resp.StatusCode)
	}
}

func TestSettingsChangeRestartsBuild(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, rampImages()...)
	release := f.GateTiles()
	ts := newTS(t, withMapillary(f))
	admin := ts.setupAdmin(t)
	id := ts.importRamp(t, admin)
	ts.setMapillaryToken(t, mlyToken)
	ts.photos(t, admin, id)
	for f.TileRequests() == 0 {
		time.Sleep(time.Millisecond)
	}
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"mapillary": map[string]any{"token": mlyToken + "-2"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	release()
	if out := ts.pollPhotos(t, admin, id); out.Status != "ready" {
		t.Fatalf("after restart: %+v", out)
	}
	row, err := ts.Store.GetRoutePhotos(context.Background(), id)
	if err != nil || row.TokenFP != ts.fp(mlyToken+"-2") {
		t.Errorf("row built under the old token: %+v %v", row, err)
	}
}
