package api

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "route", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// importFile POSTs file as multipart field "file" named filename.
func (ts *testServer) importFile(t testing.TB, c *http.Client, query, filename string, content []byte, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req, err := http.NewRequest("POST", ts.URL+"/api/routes/import"+query, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
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
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func TestImportGPX(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.importFile(t, admin, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 201 {
		t.Fatalf("import: %d %s", resp.StatusCode, b)
	}
	d := decode[RouteDetail](t, b)
	if d.Source != "gpx" || d.Name != "Ramp" || !near(d.DistanceM, 1000.75, 0.1) || !near(d.AscentM, 50, 2) || d.OwnerID == 0 ||
		len(d.GeoJSON.Coordinates) < 2 || d.GeoJSON.Type != "LineString" {
		t.Fatalf("detail = %+v", d)
	}
	resp, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/routes/%d/profile", d.ID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("profile: %d %s", resp.StatusCode, b)
	}
	p := decode[ProfileOut](t, b)
	if p.StepM != 10 || len(p.Lat) != 102 || len(p.Lon) != 102 || len(p.EleM) != 102 || len(p.GradePct) != 102 ||
		!near(p.GradePct[50], 5, 0.3) || p.RouteID != d.ID {
		t.Fatalf("profile = step %v len %d grade[50] %v", p.StepM, len(p.Lat), p.GradePct[50])
	}
}

func TestImportTCXAndFIT(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for _, f := range []string{"tcx", "fit"} {
		resp, b := ts.importFile(t, admin, "", "ramp."+f, fixture(t, "ramp."+f))
		if resp.StatusCode != 201 {
			t.Fatalf("%s: %d %s", f, resp.StatusCode, b)
		}
		d := decode[RouteDetail](t, b)
		if d.Source != f || !near(d.DistanceM, 1000.75, 1) || !near(d.AscentM, 50, 2) {
			t.Errorf("%s detail = %+v", f, d.RouteOut)
		}
	}
}

func TestImportNameQuery(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.importFile(t, admin, "?name=Morning", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 201 || decode[RouteDetail](t, b).Name != "Morning" {
		t.Fatalf("name query: %d %s", resp.StatusCode, b)
	}
}

func TestImportNameFromFilename(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	gpx := strings.Replace(string(fixture(t, "ramp.gpx")), "<name>Ramp</name>", "", -1)
	resp, b := ts.importFile(t, admin, "", "Sunday loop.gpx", []byte(gpx))
	if resp.StatusCode != 201 || decode[RouteDetail](t, b).Name != "Sunday loop" {
		t.Fatalf("filename name: %d %s", resp.StatusCode, b)
	}
}

func TestImportUnsupported(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.importFile(t, admin, "", "hello.txt", []byte("hello world"))
	if resp.StatusCode != 400 || errCode(t, b) != "unsupported_format" {
		t.Fatalf("unsupported: %d %s", resp.StatusCode, b)
	}
}

func TestImportMalformedAndNoTrack(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.importFile(t, admin, "", "x.gpx", []byte(`<gpx><trk><trkseg></trkseg></trk></gpx>`))
	if resp.StatusCode != 400 || errCode(t, b) != "no_track" {
		t.Errorf("no track: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.importFile(t, admin, "", "x.fit", []byte("garbage"))
	if resp.StatusCode != 400 {
		t.Errorf("garbage fit: %d %s", resp.StatusCode, b)
	}
}

func TestImportTooLarge(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	big := append(fixture(t, "ramp.gpx"), make([]byte, 21<<20)...)
	resp, b := ts.importFile(t, admin, "", "ramp.gpx", big)
	if resp.StatusCode != 413 || errCode(t, b) != "too_large" {
		t.Fatalf("too large: %d %.200s", resp.StatusCode, b)
	}
}

func TestImportNoElevation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.importFile(t, admin, "", "ramp_noele.gpx", fixture(t, "ramp_noele.gpx"))
	if resp.StatusCode != 422 || errCode(t, b) != "no_elevation" || !strings.Contains(string(b), "Settings → Integrations") {
		t.Fatalf("no ele: %d %s", resp.StatusCode, b)
	}
}

func otdFake(t testing.TB, ele float64, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		n := len(strings.Split(r.URL.Query().Get("locations"), "|"))
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"elevation":%v}`, ele)
		}
		fmt.Fprintf(w, `{"status":"OK","results":[%s]}`, strings.Join(parts, ","))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportHighLatUsesOpenTopoData(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	var hits atomic.Int32
	srv := otdFake(t, 500, &hits)
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{
		"elevation": map[string]any{"opentopodata": true, "opentopodata_url": srv.URL}})
	if resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	gpx := strings.ReplaceAll(string(fixture(t, "ramp.gpx")), `lat="45.`, `lat="64.`)
	resp, b = ts.importFile(t, admin, "", "ramp.gpx", []byte(gpx))
	if resp.StatusCode != 201 {
		t.Fatalf("import: %d %s", resp.StatusCode, b)
	}
	if hits.Load() < 1 {
		t.Error("OpenTopoData fake was not called")
	}
	d := decode[RouteDetail](t, b)
	if d.AscentM != 0 {
		t.Errorf("ascent = %v, want 0", d.AscentM)
	}
	_, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/routes/%d/profile", d.ID), nil)
	for i, e := range decode[ProfileOut](t, b).EleM {
		if e != 500 {
			t.Fatalf("ele_m[%d] = %v, want 500", i, e)
		}
	}
}

func TestImportHighLatWithoutOpenTopoData(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	gpx := strings.ReplaceAll(string(fixture(t, "ramp_noele.gpx")), `lat="45.`, `lat="64.`)
	resp, b := ts.importFile(t, admin, "", "x.gpx", []byte(gpx))
	if resp.StatusCode != 422 || errCode(t, b) != "no_elevation" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestImportElevationUnavailable(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom secret-detail", 500)
	}))
	defer srv.Close()
	ts.do(t, admin, "PATCH", "/api/settings", map[string]any{
		"elevation": map[string]any{"opentopodata": true, "opentopodata_url": srv.URL}})
	resp, b := ts.importFile(t, admin, "", "x.gpx", []byte(strings.ReplaceAll(string(fixture(t, "ramp_noele.gpx")), `lat="45.`, `lat="64.`)))
	if resp.StatusCode != 502 || errCode(t, b) != "elevation_unavailable" || strings.Contains(string(b), "secret-detail") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestSyntheticRoute(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	segs := []map[string]any{{"length_m": 1000, "grade_pct": 0}, {"length_m": 1000, "grade_pct": 6}}
	resp, b := ts.do(t, admin, "POST", "/api/routes/synthetic", map[string]any{"name": "Climb", "segments": segs})
	if resp.StatusCode != 201 {
		t.Fatalf("synthetic: %d %s", resp.StatusCode, b)
	}
	d := decode[RouteDetail](t, b)
	if d.Source != "synthetic" || d.Name != "Climb" || !near(d.DistanceM, 2000, 0.1) || !near(d.AscentM, 60, 2) ||
		!near(d.MaxGradePct, 6, 0.2) || len(d.Spec.Segments) != 2 || d.Spec.Segments[1].GradePct != 6 || d.Spec.Segments[0].LengthM != 1000 {
		t.Fatalf("detail = %+v", d)
	}
	resp, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/routes/%d", d.ID), nil)
	if resp.StatusCode != 200 || len(decode[RouteDetail](t, b).Spec.Segments) != 2 {
		t.Errorf("get: %d %s", resp.StatusCode, b)
	}
}

func TestSyntheticValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for name, body := range map[string]map[string]any{
		"grade 30":     {"name": "x", "segments": []map[string]any{{"length_m": 100, "grade_pct": 30}}},
		"no segments":  {"name": "x", "segments": []map[string]any{}},
		"short length": {"name": "x", "segments": []map[string]any{{"length_m": 5, "grade_pct": 1}}},
		"blank name":   {"name": "  ", "segments": []map[string]any{{"length_m": 100, "grade_pct": 1}}},
	} {
		if resp, b := ts.do(t, admin, "POST", "/api/routes/synthetic", body); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
	segs := make([]map[string]any, 6)
	for i := range segs {
		segs[i] = map[string]any{"length_m": 100000, "grade_pct": 0}
	}
	resp, b := ts.do(t, admin, "POST", "/api/routes/synthetic", map[string]any{"name": "x", "segments": segs})
	if resp.StatusCode != 422 || errCode(t, b) != "route_too_long" {
		t.Errorf("600 km: %d %s", resp.StatusCode, b)
	}
}

func TestRoutesListAndOwnership(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", testPW, false)
	bob := ts.loginAs(t, "bob", testPW)
	resp, b := ts.importFile(t, admin, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode, string(b))
	}
	adminRoute := decode[RouteDetail](t, b)
	resp, b = ts.importFile(t, bob, "?name=Bobs", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode, string(b))
	}
	bobRoute := decode[RouteDetail](t, b)

	// list: newest first, visible to all
	resp, b = ts.do(t, bob, "GET", "/api/routes", nil)
	rs := decode[[]RouteOut](t, b)
	if resp.StatusCode != 200 || len(rs) != 2 || rs[0].ID != bobRoute.ID || rs[1].ID != adminRoute.ID {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}
	ap := fmt.Sprintf("/api/routes/%d", adminRoute.ID)
	if resp, _ = ts.do(t, bob, "GET", ap, nil); resp.StatusCode != 200 {
		t.Errorf("bob GET: %d", resp.StatusCode)
	}
	resp, b = ts.do(t, bob, "PATCH", ap, map[string]any{"name": "Mine now"})
	if resp.StatusCode != 403 || errCode(t, b) != "not_owner" {
		t.Errorf("bob PATCH: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, bob, "DELETE", ap, nil)
	if resp.StatusCode != 403 || errCode(t, b) != "not_owner" {
		t.Errorf("bob DELETE: %d %s", resp.StatusCode, b)
	}
	// owner renames
	bp := fmt.Sprintf("/api/routes/%d", bobRoute.ID)
	resp, b = ts.do(t, bob, "PATCH", bp, map[string]any{"name": "Renamed"})
	if resp.StatusCode != 200 || decode[RouteDetail](t, b).Name != "Renamed" {
		t.Errorf("owner PATCH: %d %s", resp.StatusCode, b)
	}
	// admin acts on bob's route
	resp, b = ts.do(t, admin, "PATCH", bp, map[string]any{"name": "By admin"})
	if resp.StatusCode != 200 || decode[RouteDetail](t, b).Name != "By admin" {
		t.Errorf("admin PATCH: %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, admin, "PATCH", bp, map[string]any{"name": "  "}); resp.StatusCode != 400 {
		t.Errorf("blank rename: %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, admin, "DELETE", bp, nil); resp.StatusCode != 204 {
		t.Errorf("admin DELETE: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, bob, "GET", bp, nil); resp.StatusCode != 404 {
		t.Errorf("GET deleted: %d", resp.StatusCode)
	}
	if resp, b = ts.do(t, admin, "DELETE", bp, nil); resp.StatusCode != 404 {
		t.Errorf("DELETE twice: %d %s", resp.StatusCode, b)
	}
}

func TestOrphanRouteAdminOnly(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	u := ts.addUser(t, "bob", testPW, false)
	bob := ts.loginAs(t, "bob", testPW)
	_, b := ts.importFile(t, bob, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	d := decode[RouteDetail](t, b)
	if _, err := ts.Store.Write.Exec(`DELETE FROM users WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/routes/%d", d.ID)
	resp, b := ts.do(t, admin, "GET", path, nil)
	if resp.StatusCode != 200 || decode[RouteDetail](t, b).OwnerID != 0 {
		t.Fatalf("orphan GET: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Errorf("admin deletes orphan: %d", resp.StatusCode)
	}
}

func TestRouteNotFound(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for _, p := range []string{"/api/routes/999", "/api/routes/999/profile"} {
		resp, b := ts.do(t, admin, "GET", p, nil)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("%s: %d %s", p, resp.StatusCode, b)
		}
	}
}

func TestRouteInUse(t *testing.T) {
	t.Parallel()
	var busy atomic.Int64
	ts := newTS(t, withRouteInUse(func(id int64) bool { return id == busy.Load() }))
	admin := ts.setupAdmin(t)
	_, b := ts.importFile(t, admin, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	d := decode[RouteDetail](t, b)
	busy.Store(d.ID)
	path := fmt.Sprintf("/api/routes/%d", d.ID)
	resp, b := ts.do(t, admin, "DELETE", path, nil)
	if resp.StatusCode != 409 || errCode(t, b) != "route_in_use" {
		t.Fatalf("in use: %d %s", resp.StatusCode, b)
	}
	busy.Store(0)
	if resp, _ = ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Errorf("after release: %d", resp.StatusCode)
	}
}

func TestProfileGzip(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, b := ts.importFile(t, admin, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	d := decode[RouteDetail](t, b)
	resp, raw := ts.do(t, admin, "GET", fmt.Sprintf("/api/routes/%d/profile", d.ID), nil, "Accept-Encoding", "gzip")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("%d enc=%q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if p := decode[ProfileOut](t, plain); len(p.Lat) != 102 {
		t.Errorf("len = %d", len(p.Lat))
	}
}

func TestReadTokenCannotImport(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "reader", false, "read")
	resp, b := ts.importFile(t, nil, "", "ramp.gpx", fixture(t, "ramp.gpx"), bearer(tok)...)
	if resp.StatusCode != 403 {
		t.Fatalf("read token import: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, nil, "GET", "/api/routes", nil, bearer(tok)...)
	if resp.StatusCode != 200 {
		t.Errorf("read token list: %d %s", resp.StatusCode, b)
	}
	resp, _ = ts.do(t, nil, "POST", "/api/routes/synthetic", map[string]any{"name": "x",
		"segments": []map[string]any{{"length_m": 100, "grade_pct": 1}}}, bearer(tok)...)
	if resp.StatusCode != 403 {
		t.Errorf("read token synthetic: %d", resp.StatusCode)
	}
}

func TestImportRequiresLogin(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	resp, _ := ts.importFile(t, nil, "", "ramp.gpx", fixture(t, "ramp.gpx"))
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous import: %d", resp.StatusCode)
	}
}
