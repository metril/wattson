package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/metril/wattson/internal/settings"
)

func TestMapsTiles(t *testing.T) {
	t.Parallel()
	seg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<a href="E5_N45.rd5">E5_N45.rd5</a><a href="W5_N50.rd5">x</a>`))
		case "/E5_N45.rd5":
			_, _ = w.Write(make([]byte, 4096))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(seg.Close)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	dir := segDir(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir, b.SegmentsURL = dir, seg.URL+"/" })

	resp, b := ts.do(t, admin, "POST", "/api/maps/tiles/E5_N45", nil)
	if resp.StatusCode != 202 || decode[TileOut](t, b).Name != "E5_N45" {
		t.Fatalf("post: %d %s", resp.StatusCode, b)
	}
	var out TilesOut
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, b = ts.do(t, admin, "GET", "/api/maps/tiles", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("get: %d %s", resp.StatusCode, b)
		}
		out = decode[TilesOut](t, b)
		if len(out.Tiles) == 1 && out.Tiles[0].Status == "present" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tile never present: %s", b)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if out.Tiles[0].Bytes != 4096 || !out.Dir.Exists || !out.Dir.Writable || out.Dir.Path != dir ||
		len(out.Available) != 2 || out.Available[0] != "E5_N45" || out.AvailableError != "" {
		t.Errorf("list = %+v", out)
	}

	if resp, b = ts.do(t, admin, "DELETE", "/api/maps/tiles/E5_N45", nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	_, b = ts.do(t, admin, "GET", "/api/maps/tiles", nil)
	if out = decode[TilesOut](t, b); len(out.Tiles) != 0 {
		t.Errorf("after delete: %+v", out.Tiles)
	}

	resp, b = ts.do(t, admin, "POST", "/api/maps/tiles/E7_N45", nil)
	if resp.StatusCode != 400 || decode[errEnvelope](t, b).Error.Code != "bad_tile_name" {
		t.Errorf("bad name: %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, admin, "DELETE", "/api/maps/tiles/..%2Fetc", nil); resp.StatusCode != 400 && resp.StatusCode != 404 {
		t.Errorf("traversal: %d %s", resp.StatusCode, b)
	}

	tok := "Bearer " + ts.mintToken(t, "bob", false, "read", "control")
	for _, m := range [][2]string{{"GET", "/api/maps/tiles"}, {"POST", "/api/maps/tiles/E5_N45"}, {"DELETE", "/api/maps/tiles/E5_N45"}} {
		if resp, _ := ts.do(t, nil, m[0], m[1], nil, "Authorization", tok); resp.StatusCode != 403 {
			t.Errorf("bob %s %s: %d", m[0], m[1], resp.StatusCode)
		}
	}

	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir = "/nonexistent-wattson" })
	resp, b = ts.do(t, admin, "POST", "/api/maps/tiles/E5_N45", nil)
	if resp.StatusCode != 409 || decode[errEnvelope](t, b).Error.Code != "segments_dir_unavailable" {
		t.Errorf("missing dir: %d %s", resp.StatusCode, b)
	}
	_, b = ts.do(t, admin, "GET", "/api/maps/tiles", nil)
	if out = decode[TilesOut](t, b); out.Dir.Exists {
		t.Errorf("missing dir listed as existing: %+v", out.Dir)
	}
}

func TestMapsAvailableError(t *testing.T) {
	t.Parallel()
	seg := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(seg.Close)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.setBRouter(t, func(b *settings.BRouter) { b.SegmentsDir, b.SegmentsURL = segDir(t), seg.URL+"/" })
	_, b := ts.do(t, admin, "GET", "/api/maps/tiles", nil)
	out := decode[TilesOut](t, b)
	if out.AvailableError == "" || out.Available == nil || len(out.Available) != 0 {
		t.Errorf("list = %+v", out)
	}
}
