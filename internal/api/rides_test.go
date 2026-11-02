package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/filedef"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

func TestListRidesPaged(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ar, br := ts.riderOf(t, admin), ts.riderOf(t, bob)
	var ids []int64
	for range 25 {
		ids = append(ids, ts.seedRide(t, ar, 12, false).ID)
	}
	ts.seedRide(t, br, 12, false)
	resp, b := ts.do(t, admin, "GET", "/api/rides", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}
	p1 := decode[RideList](t, b)
	if len(p1.Rides) != 20 || p1.NextBefore != p1.Rides[19].ID || p1.Rides[0].ID != ids[24] {
		t.Fatalf("page 1: %d rides, next %d", len(p1.Rides), p1.NextBefore)
	}
	for _, r := range p1.Rides {
		if r.RiderID != ar || r.RiderName != "admin" || r.Strava.Status != "none" || r.EndedAt == nil || r.SampleCount != 12 {
			t.Fatalf("ride = %+v", r)
		}
	}
	_, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/rides?before=%d", p1.NextBefore), nil)
	p2 := decode[RideList](t, b)
	if len(p2.Rides) != 5 || p2.NextBefore != 0 || p2.Rides[4].ID != ids[0] {
		t.Fatalf("page 2: %d rides, next %d", len(p2.Rides), p2.NextBefore)
	}
	// An exact multiple of the page size ends with next_before 0.
	_, b = ts.do(t, admin, "GET", "/api/rides?limit=25", nil)
	if p := decode[RideList](t, b); len(p.Rides) != 25 || p.NextBefore != 0 {
		t.Errorf("limit 25: %d rides, next %d", len(p.Rides), p.NextBefore)
	}
	for _, q := range []string{"limit=0", "limit=101", "before=-1"} {
		if resp, b = ts.do(t, admin, "GET", "/api/rides?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s, want 400", q, resp.StatusCode, b)
		}
	}
	// Admin sees bob's rides only by naming his rider.
	_, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/rides?rider_id=%d", br), nil)
	if p := decode[RideList](t, b); len(p.Rides) != 1 || p.Rides[0].RiderID != br {
		t.Errorf("admin rider_id=bob: %s", b)
	}
}

func TestListRidesOtherRider404(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ar := ts.riderOf(t, admin)
	ts.seedRide(t, ar, 12, false)
	for _, id := range []int64{ar, 9999} {
		resp, b := ts.do(t, bob, "GET", fmt.Sprintf("/api/rides?rider_id=%d", id), nil)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("bob rider_id=%d: %d %s", id, resp.StatusCode, b)
		}
	}
	// Bob's own list does not include the admin's ride.
	_, b := ts.do(t, bob, "GET", "/api/rides", nil)
	if p := decode[RideList](t, b); len(p.Rides) != 0 {
		t.Errorf("bob list = %s", b)
	}
}

func TestRideDetailSeries(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 3600, true)
	path := fmt.Sprintf("/api/rides/%d", r.ID)
	resp, b := ts.do(t, admin, "GET", path, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("detail: %d", resp.StatusCode)
	}
	d := decode[RideDetail](t, b)
	if len(d.Series.PowerW) != 600 || len(d.Series.Lat) != 600 || len(d.Series.Lon) != 600 || len(d.Series.EleM) != 600 ||
		len(d.Series.HRBpm) != 600 || len(d.Series.Level) != 600 {
		t.Errorf("series lengths: power %d lat %d", len(d.Series.PowerW), len(d.Series.Lat))
	}
	if d.NPW <= 0 || d.ID != r.ID || d.RouteName != "Loop" || d.Series.ElapsedS[599] != 3600 {
		t.Errorf("detail = %+v", d.RideOut)
	}
	if !strings.Contains(string(b), `"np_w"`) {
		t.Error("np_w missing")
	}
	_, b = ts.do(t, admin, "GET", path+"?points=50", nil)
	if d = decode[RideDetail](t, b); len(d.Series.PowerW) != 50 || len(d.Series.Lat) != 50 {
		t.Errorf("points=50: %d", len(d.Series.PowerW))
	}
	for _, q := range []string{"points=49", "points=3601"} {
		if resp, b = ts.do(t, admin, "GET", path+"?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", q, resp.StatusCode, b)
		}
	}
	// A short ride is returned whole (raw path).
	short := ts.seedRide(t, ts.riderOf(t, admin), 120, true)
	_, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d", short.ID), nil)
	if d = decode[RideDetail](t, b); len(d.Series.PowerW) != 120 || len(d.Series.Lat) != 120 {
		t.Errorf("raw: %d", len(d.Series.PowerW))
	}
}

func TestRideDetailFreeRideOmitsPosition(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	// 120 samples take the raw path, 3600 the downsampled one.
	for _, n := range []int{120, 3600} {
		r := ts.seedRide(t, rider, n, false)
		resp, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d", r.ID), nil)
		if resp.StatusCode != 200 {
			t.Fatalf("n=%d: %d %s", n, resp.StatusCode, b)
		}
		s := string(b)
		for _, bad := range []string{`"lat"`, `"lon"`, `"ele_m"`, "NaN", "Inf"} {
			if strings.Contains(s, bad) {
				t.Errorf("n=%d: body contains %s", n, bad)
			}
		}
	}
}

func TestRideDetailPartialPositionOmitted(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 120, true)
	// Drop the position of one sample: the arrays must disappear, not carry NaN.
	if _, err := ts.Store.Write.Exec(`UPDATE samples SET lat=NULL, lon=NULL, ele_m=NULL WHERE ride_id=? AND seq=5`, r.ID); err != nil {
		t.Fatal(err)
	}
	_, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d", r.ID), nil)
	if s := string(b); strings.Contains(s, `"lat"`) || strings.Contains(s, "NaN") || strings.Contains(s, `"ele_m"`) {
		t.Errorf("partial position leaked: %s", s)
	}
}

func TestRideFit(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 120, true)
	resp, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d/fit", r.ID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("fit: %d %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.ant.fit" {
		t.Errorf("Content-Type = %q", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !regexp.MustCompile(`^attachment; filename="wattson-\d+-\d{8}-\d{4}\.fit"$`).MatchString(cd) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if len(b) < 12 || string(b[8:12]) != ".FIT" {
		t.Fatalf("not a FIT file: % x", b[:min(len(b), 16)])
	}
	lis := filedef.NewListener()
	defer lis.Close()
	dec := decoder.New(bytes.NewReader(b), decoder.WithMesgListener(lis), decoder.WithBroadcastOnly())
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	act, ok := lis.File().(*filedef.Activity)
	if !ok || len(act.Records) != 120 {
		t.Errorf("records = %v", ok && act != nil)
	}
}

func TestRideFitWorksWithoutStrava(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 30, false)
	if resp, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d/fit", r.ID), nil); resp.StatusCode != 200 || len(b) == 0 {
		t.Errorf("fit: %d", resp.StatusCode)
	}
}

func TestRideFitKioskReadToken(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "kiosk", false, "read")
	u, err := ts.Store.UserByUsername(context.Background(), "kiosk")
	if err != nil {
		t.Fatal(err)
	}
	rd, err := ts.Store.CreateRider(context.Background(), store.DefaultRider(u.ID, "k"))
	if err != nil {
		t.Fatal(err)
	}
	r := ts.seedRide(t, rd.ID, 30, false)
	auth := "Bearer " + tok
	if resp, b := ts.do(t, nil, "GET", fmt.Sprintf("/api/rides/%d/fit", r.ID), nil, "Authorization", auth); resp.StatusCode != 200 {
		t.Errorf("fit with read token: %d %s", resp.StatusCode, b)
	}
	if resp, b := ts.do(t, nil, "DELETE", fmt.Sprintf("/api/rides/%d", r.ID), nil, "Authorization", auth); resp.StatusCode != 403 {
		t.Errorf("delete with read token: %d %s", resp.StatusCode, b)
	}
}

func TestRideOwnership(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ar, br := ts.riderOf(t, admin), ts.riderOf(t, bob)
	mine := ts.seedRide(t, ar, 30, false)
	theirs := ts.seedRide(t, br, 30, false)
	for _, tc := range []struct{ method, suffix string }{{"GET", ""}, {"GET", "/fit"}, {"DELETE", ""}} {
		resp, b := ts.do(t, bob, tc.method, fmt.Sprintf("/api/rides/%d%s", mine.ID, tc.suffix), nil)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("bob %s%s: %d %s, want 404", tc.method, tc.suffix, resp.StatusCode, b)
		}
	}
	if resp, _ := ts.do(t, bob, "GET", "/api/rides/99999", nil); resp.StatusCode != 404 {
		t.Errorf("missing ride: %d", resp.StatusCode)
	}
	if _, err := ts.Store.Ride(context.Background(), mine.ID); err != nil {
		t.Errorf("admin ride deleted by bob: %v", err)
	}
	for _, suffix := range []string{"", "/fit"} {
		if resp, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d%s", theirs.ID, suffix), nil); resp.StatusCode != 200 {
			t.Errorf("admin GET bob's %s: %d %s", suffix, resp.StatusCode, b)
		}
	}
	if resp, b := ts.do(t, admin, "DELETE", fmt.Sprintf("/api/rides/%d", theirs.ID), nil); resp.StatusCode != 204 {
		t.Errorf("admin DELETE bob's: %d %s", resp.StatusCode, b)
	}
}

func TestRideInProgress(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r, err := ts.Store.CreateRide(context.Background(),
		store.Ride{RiderID: ts.riderOf(t, admin), Mode: "free", StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, suffix string }{{"GET", ""}, {"GET", "/fit"}, {"DELETE", ""}} {
		resp, b := ts.do(t, admin, tc.method, fmt.Sprintf("/api/rides/%d%s", r.ID, tc.suffix), nil)
		if resp.StatusCode != 409 || errCode(t, b) != "ride_in_progress" {
			t.Errorf("%s%s: %d %s, want 409 ride_in_progress", tc.method, tc.suffix, resp.StatusCode, b)
		}
	}
	_, b := ts.do(t, admin, "GET", "/api/rides", nil)
	if p := decode[RideList](t, b); len(p.Rides) != 0 {
		t.Errorf("open ride listed: %s", b)
	}
}

func TestDeleteRide(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 30, false)
	path := fmt.Sprintf("/api/rides/%d", r.ID)
	if resp, b := ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if resp, _ := ts.do(t, admin, "GET", path, nil); resp.StatusCode != 404 {
		t.Errorf("get after delete: %d", resp.StatusCode)
	}
	if ss, err := ts.Store.Samples(context.Background(), r.ID); err != nil || len(ss) != 0 {
		t.Errorf("samples after delete: %d, %v", len(ss), err)
	}
}

func TestOpenAPIRides(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	spec := decode[map[string]any](t, b)
	get := func(v any, keys ...string) map[string]any {
		t.Helper()
		for _, k := range keys {
			m, ok := v.(map[string]any)
			if !ok || m[k] == nil {
				t.Fatalf("spec has no %q", k)
			}
			v = m[k]
		}
		return v.(map[string]any)
	}
	fit := get(spec, "paths", "/api/rides/{id}/fit", "get", "responses", "200", "content", "application/vnd.ant.fit", "schema")
	if fit["format"] != "binary" || fit["type"] != "string" {
		t.Errorf("fit schema = %v", fit)
	}
	if c := get(spec, "paths", "/api/rides/{id}/fit", "get", "responses", "200", "content"); len(c) != 1 {
		t.Errorf("fit content types = %v", c)
	}
	for _, p := range []string{"/api/rides", "/api/rides/{id}"} {
		get(spec, "paths", p)
	}
}

func TestAPICacheControlNoStore(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	r := ts.seedRide(t, ts.riderOf(t, admin), 30, false)
	for _, path := range []string{"/api/rides", fmt.Sprintf("/api/rides/%d/fit", r.ID), "/api/nope"} {
		resp, _ := ts.do(t, admin, "GET", path, nil)
		if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
	}
}

func TestListRidesRefusesUserlessIdentity(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.seedRide(t, ts.riderOf(t, ts.setupAdmin(t)), 12, false)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id := auth.Identity{Scopes: []auth.Scope{auth.ScopeRead}} // User.ID == 0
			next.ServeHTTP(w, req.WithContext(auth.NewContext(req.Context(), id)))
		})
	})
	Deps{Store: ts.Store}.registerRides(newHumaAPI(r))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/rides", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), `"rides":[{`) {
		t.Errorf("userless list = %d %s, want 500 and no rides", rec.Code, rec.Body)
	}
}
