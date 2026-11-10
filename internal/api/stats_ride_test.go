package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/metril/wattson/internal/stats"
)

func TestRidePowerCurve(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	r := ts.seedStatsRide(t, ts.riderOf(t, admin), time.Now().UTC().Add(-time.Hour), 10,
		[]stats.Point{{DurS: 1, Watts: 500}, {DurS: 60, Watts: 300}})
	path := fmt.Sprintf("/api/rides/%d/power-curve", r.ID)

	resp, b := ts.do(t, admin, "GET", path, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("curve: %d %s", resp.StatusCode, b)
	}
	if out := decode[RideCurveOut](t, b); len(out.Points) != 2 || out.Points[1] != (RideCurvePoint{DurS: 60, Watts: 300}) {
		t.Errorf("curve = %+v", out)
	}
	if resp, _ = ts.do(t, bob, "GET", path, nil); resp.StatusCode != 404 {
		t.Errorf("other user: %d, want 404", resp.StatusCode)
	}
	if resp, _ = ts.do(t, admin, "GET", "/api/rides/9999/power-curve", nil); resp.StatusCode != 404 {
		t.Errorf("missing: %d, want 404", resp.StatusCode)
	}
}
