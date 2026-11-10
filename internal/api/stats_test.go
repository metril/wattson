package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/metril/wattson/internal/stats"
	"github.com/metril/wattson/internal/store"
)

// seedStatsRide records a finished ride starting at at, with a TSS and power curve.
func (ts *testServer) seedStatsRide(t testing.TB, riderID int64, at time.Time, tss float64, curve []stats.Point) store.Ride {
	t.Helper()
	ctx := t.Context()
	r, err := ts.Store.CreateRide(ctx, store.Ride{RiderID: riderID, Mode: "free", StartedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	sum := store.Summary{DurationS: 1800, DistanceM: 15000, AscentM: 50, KJ: 300, TSS: &tss, Curve: curve, StatsVersion: store.StatsVersion}
	r, err = ts.Store.FinishRide(ctx, r.ID, at.Add(30*time.Minute), sum, 0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStatsTotalsAndStreaks(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	now := time.Now().UTC()
	for i := range 3 { // today, yesterday, the day before
		ts.seedStatsRide(t, rider, now.Add(-time.Duration(i)*24*time.Hour), 40, nil)
	}
	resp, b := ts.do(t, admin, "GET", "/api/stats/totals?period=month&tz=UTC", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("totals: %d %s", resp.StatusCode, b)
	}
	out := decode[TotalsOut](t, b)
	if out.Streaks.CurrentDays != 3 || out.Streaks.LongestDays != 3 || out.Streaks.CurrentWeeks < 1 {
		t.Errorf("streaks = %+v", out.Streaks)
	}
	var rides int
	var tss float64
	for _, p := range out.Periods {
		rides += p.Rides
		tss += p.TSS
	}
	if out.Period != "month" || out.TZ != "UTC" || rides != 3 || tss != 120 {
		t.Errorf("totals = %+v", out)
	}
	if last := out.Periods[len(out.Periods)-1]; last.Start != now.Format("2006-01")+"-01" {
		t.Errorf("last period = %q", last.Start)
	}

	// Defaults and validation.
	if resp, b = ts.do(t, admin, "GET", "/api/stats/totals", nil); resp.StatusCode != 200 || decode[TotalsOut](t, b).Period != "week" {
		t.Errorf("defaults: %d %s", resp.StatusCode, b)
	}
	for _, q := range []string{"period=day", "tz=Mars/Base", "tz=Local", "rider_id=-1"} {
		if resp, b = ts.do(t, admin, "GET", "/api/stats/totals?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s, want 400", q, resp.StatusCode, b)
		}
	}
}

func TestStatsEmptyHistory(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, b := ts.do(t, admin, "GET", "/api/stats/totals?period=year", nil)
	out := decode[TotalsOut](t, b)
	if len(out.Periods) != 1 || out.Periods[0].Rides != 0 || out.Streaks != (Streaks{}) {
		t.Errorf("totals = %+v", out)
	}
	_, b = ts.do(t, admin, "GET", "/api/stats/fitness", nil)
	if f := decode[FitnessOut](t, b); f.Days == nil || len(f.Days) != 0 {
		t.Errorf("fitness = %+v", f)
	}
	_, b = ts.do(t, admin, "GET", "/api/stats/power-curve", nil)
	if c := decode[PowerCurveOut](t, b); len(c.Points) != 0 || c.EFTPW != nil {
		t.Errorf("curve = %+v", c)
	}
}

func TestStatsFitness(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	now := time.Now().UTC()
	ts.seedStatsRide(t, rider, now.AddDate(0, 0, -10), 100, nil)
	resp, b := ts.do(t, admin, "GET", "/api/stats/fitness?days=7", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("fitness: %d %s", resp.StatusCode, b)
	}
	f := decode[FitnessOut](t, b)
	if len(f.Days) != 7 {
		t.Fatalf("days = %d", len(f.Days))
	}
	last := f.Days[6]
	if last.Date != now.Format(time.DateOnly) || last.TSS != 0 || last.CTL <= 0 || last.ATL >= last.CTL+100 || last.TSB != last.CTL-last.ATL {
		t.Errorf("last day = %+v", last)
	}
	for _, q := range []string{"days=6", "days=731", "tz=nope"} {
		if resp, _ = ts.do(t, admin, "GET", "/api/stats/fitness?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestStatsPowerCurveWindowsAndOwnership(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ar, br := ts.riderOf(t, admin), ts.riderOf(t, bob)
	now := time.Now().UTC()
	old := ts.seedStatsRide(t, ar, now.AddDate(0, 0, -200), 0, []stats.Point{{DurS: 5, Watts: 900}, {DurS: 1200, Watts: 300}})
	mid := ts.seedStatsRide(t, ar, now.AddDate(0, 0, -30), 0, []stats.Point{{DurS: 5, Watts: 800}, {DurS: 1200, Watts: 250}})
	ts.seedStatsRide(t, ar, now.AddDate(0, 0, -2), 0, []stats.Point{{DurS: 5, Watts: 700}})
	ts.seedStatsRide(t, br, now.AddDate(0, 0, -1), 0, []stats.Point{{DurS: 5, Watts: 2000}})

	get := func(q string) PowerCurveOut {
		t.Helper()
		resp, b := ts.do(t, admin, "GET", "/api/stats/power-curve"+q, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", q, resp.StatusCode, b)
		}
		return decode[PowerCurveOut](t, b)
	}
	all := get("")
	if len(all.Points) != 2 || all.Points[0].Watts != 900 || all.Points[0].RideID != old.ID || all.Points[0].WPerKg <= 0 {
		t.Errorf("all = %+v", all)
	}
	if all.EFTPW == nil || *all.EFTPW != 285 {
		t.Errorf("eftp = %v", all.EFTPW)
	}
	y := get("?window=1y")
	if y.Window != "1y" || y.Points[0].Watts != 900 {
		t.Errorf("1y = %+v", y)
	}
	n := get("?window=90d")
	if n.Points[0].Watts != 800 || n.Points[0].RideID != mid.ID || *n.EFTPW != 237.5 {
		t.Errorf("90d = %+v", n)
	}
	// Bob's 2000 W never leaks into the admin's curve, and admin may read bob's rider.
	if b := get(fmt.Sprintf("?rider_id=%d", br)); len(b.Points) != 1 || b.Points[0].Watts != 2000 || b.EFTPW != nil {
		t.Errorf("bob = %+v", b)
	}
	// Bob cannot read the admin's rider.
	if resp, _ := ts.do(t, bob, "GET", fmt.Sprintf("/api/stats/power-curve?rider_id=%d", ar), nil); resp.StatusCode != 404 {
		t.Errorf("other rider: %d, want 404", resp.StatusCode)
	}
	if resp, _ := ts.do(t, admin, "GET", "/api/stats/power-curve?window=5y", nil); resp.StatusCode != 400 {
		t.Errorf("bad window: %d", resp.StatusCode)
	}
	if resp, _ := ts.do(t, &http.Client{}, "GET", "/api/stats/totals", nil); resp.StatusCode != 401 {
		t.Errorf("anonymous: %d", resp.StatusCode)
	}
}
