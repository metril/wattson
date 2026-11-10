package api

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
)

// seedRouteRide stores a finished ride on routeID that takes elapsed seconds
// to cover 100 m; completed rides carry the Completed flag.
func (ts *testServer) seedRouteRide(t testing.TB, riderID, routeID int64, startMin int, elapsed int, completed bool) store.Ride {
	t.Helper()
	ctx := context.Background()
	t0 := time.Date(2026, 5, 1, 8, startMin, 0, 0, time.UTC)
	r, err := ts.Store.CreateRide(ctx, store.Ride{RiderID: riderID, RouteID: routeID, Mode: "route", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	ss := make([]store.Sample, elapsed)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: 100 * float64(i+1) / float64(elapsed), PowerW: 150, Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
	}
	if err := ts.Store.AppendSamples(ctx, r.ID, ss); err != nil {
		t.Fatal(err)
	}
	sum := store.Summary{ElapsedS: float64(elapsed), DurationS: float64(elapsed), DistanceM: 100, AvgPowerW: 150,
		NPW: 160, AvgSpeedKmh: 360 / float64(elapsed), Completed: completed, StatsVersion: store.StatsVersion}
	r, err = ts.Store.FinishRide(ctx, r.ID, ss[elapsed-1].At, sum, elapsed)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRouteStatsAndGhost(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ar, br := ts.riderOf(t, admin), ts.riderOf(t, bob)
	rt, err := ts.Store.CreateRoute(context.Background(), store.Route{UserID: 1, Name: "R", Source: "gpx", DistanceM: 100,
		BBox: [4]float64{1, 2, 3, 4}, GeoJSON: []byte(`{"type":"LineString","coordinates":[]}`), Spec: []byte(`{}`), Points: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/routes/%d", rt.ID)

	// No rides yet: empty stats, no ghost.
	resp, b := ts.do(t, admin, "GET", base+"/stats", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("stats: %d %s", resp.StatusCode, b)
	}
	if s := decode[RouteStatsOut](t, b); s.Attempts != 0 || s.BestS != nil || len(s.List) != 0 {
		t.Fatalf("empty = %+v", s)
	}
	if resp, _ = ts.do(t, admin, "GET", base+"/ghost", nil); resp.StatusCode != 404 {
		t.Fatalf("ghost without pr: %d", resp.StatusCode)
	}

	ts.seedRouteRide(t, ar, rt.ID, 0, 60, true)
	pr := ts.seedRouteRide(t, ar, rt.ID, 10, 40, true)
	ts.seedRouteRide(t, ar, rt.ID, 20, 20, false) // fast but not completed
	ts.seedRouteRide(t, br, rt.ID, 30, 10, true)  // someone else's

	_, b = ts.do(t, admin, "GET", base+"/stats", nil)
	s := decode[RouteStatsOut](t, b)
	if s.Attempts != 3 || s.Completed != 2 || *s.BestS != 40 || *s.AvgS != 50 || *s.LastS != 40 || s.PRRideID != pr.ID {
		t.Fatalf("stats = %+v", s)
	}
	if !s.List[1].IsPR || s.List[0].IsPR || s.List[2].IsPR || s.List[2].Completed {
		t.Fatalf("list = %+v", s.List)
	}

	resp, b = ts.do(t, admin, "GET", base+"/ghost", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("ghost: %d %s", resp.StatusCode, b)
	}
	g := decode[GhostOut](t, b)
	if g.TimeS != 40 || len(g.Points) < 2 || g.Points[len(g.Points)-1][0] != 100 {
		t.Fatalf("ghost = %+v", g)
	}

	// Another user's rider is hidden; bob sees only his own.
	if resp, _ = ts.do(t, bob, "GET", fmt.Sprintf("%s/stats?rider_id=%d", base, ar), nil); resp.StatusCode != 404 {
		t.Fatalf("foreign rider: %d", resp.StatusCode)
	}
	_, b = ts.do(t, bob, "GET", base+"/stats", nil)
	if s := decode[RouteStatsOut](t, b); s.Attempts != 1 || *s.BestS != 10 {
		t.Fatalf("bob = %+v", s)
	}
	if resp, _ = ts.do(t, admin, "GET", "/api/routes/9999/stats", nil); resp.StatusCode != 404 {
		t.Fatalf("missing route: %d", resp.StatusCode)
	}
}
