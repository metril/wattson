package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/stats"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

// seedStatsRide records a finished ride with the given start, TSS and power curve.
func seedStatsRide(tb testing.TB, ctx context.Context, s *store.Store, riderID int64, at time.Time, tss float64, curve []stats.Point) store.Ride {
	tb.Helper()
	r, err := s.CreateRide(ctx, store.Ride{RiderID: riderID, Mode: "free", StartedAt: at})
	if err != nil {
		tb.Fatal(err)
	}
	sum := store.Summary{DurationS: 3600, DistanceM: 30000, AscentM: 100, KJ: 600, TSS: &tss, Curve: curve, StatsVersion: store.StatsVersion}
	r, err = s.FinishRide(ctx, r.ID, at.Add(time.Hour), sum, 0)
	if err != nil {
		tb.Fatal(err)
	}
	return r
}

func statsFixture(tb testing.TB) (*store.Store, store.User, store.Rider, store.Rider) {
	tb.Helper()
	ctx := context.Background()
	s := storetest.New(tb)
	u, err := s.CreateUser(ctx, store.User{Username: "u"})
	if err != nil {
		tb.Fatal(err)
	}
	a, err := s.CreateRider(ctx, store.DefaultRider(u.ID, "A"))
	if err != nil {
		tb.Fatal(err)
	}
	b, err := s.CreateRider(ctx, store.DefaultRider(u.ID, "B"))
	if err != nil {
		tb.Fatal(err)
	}
	return s, u, a, b
}

func TestRideBuckets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, u, a, b := statsFixture(t)
	t0 := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	seedStatsRide(t, ctx, s, a.ID, t0, 50, nil)
	seedStatsRide(t, ctx, s, a.ID, t0.Add(5*time.Minute), 30, nil) // same 15-minute slot
	seedStatsRide(t, ctx, s, a.ID, t0.Add(20*time.Minute), 10, nil)
	seedStatsRide(t, ctx, s, b.ID, t0.Add(24*time.Hour), 5, nil)
	if _, err := s.CreateRide(ctx, store.Ride{RiderID: a.ID, Mode: "free", StartedAt: t0.Add(48 * time.Hour)}); err != nil { // still recording
		t.Fatal(err)
	}

	all, err := s.RideBuckets(ctx, u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Rides != 2 || all[0].TSS != 80 || all[0].DistanceM != 60000 || !all[0].At.Equal(t0) {
		t.Fatalf("all = %+v", all)
	}
	one, err := s.RideBuckets(ctx, u.ID, b.ID)
	if err != nil || len(one) != 1 || one[0].Rides != 1 {
		t.Fatalf("rider b = %+v %v", one, err)
	}
	none, err := s.RideBuckets(ctx, u.ID+99, 0)
	if err != nil || len(none) != 0 {
		t.Fatalf("other user = %+v %v", none, err)
	}
}

func TestBestPowerCurveWindows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, u, a, b := statsFixture(t)
	now := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	old := seedStatsRide(t, ctx, s, a.ID, now.AddDate(0, 0, -200), 0, []stats.Point{{DurS: 5, Watts: 900}, {DurS: 60, Watts: 400}})
	mid := seedStatsRide(t, ctx, s, a.ID, now.AddDate(0, 0, -30), 0, []stats.Point{{DurS: 5, Watts: 800}, {DurS: 60, Watts: 420}, {DurS: 1200, Watts: 250}})
	recent := seedStatsRide(t, ctx, s, a.ID, now.AddDate(0, 0, -2), 0, []stats.Point{{DurS: 5, Watts: 700}, {DurS: 60, Watts: 410}})
	other := seedStatsRide(t, ctx, s, b.ID, now.AddDate(0, 0, -1), 0, []stats.Point{{DurS: 5, Watts: 1000}})

	tests := []struct {
		name    string
		rider   int64
		since   time.Time
		want    map[int]int64 // duration -> ride id
		watts   map[int]float64
		missing []int
	}{
		{"all time rider a", a.ID, time.Time{}, map[int]int64{5: old.ID, 60: mid.ID, 1200: mid.ID}, map[int]float64{5: 900, 60: 420, 1200: 250}, nil},
		{"last year", a.ID, now.AddDate(-1, 0, 0), map[int]int64{5: old.ID, 60: mid.ID}, nil, nil},
		{"90 days", a.ID, now.AddDate(0, 0, -90), map[int]int64{5: mid.ID, 60: mid.ID, 1200: mid.ID}, map[int]float64{5: 800}, nil},
		{"7 days", a.ID, now.AddDate(0, 0, -7), map[int]int64{5: recent.ID, 60: recent.ID}, nil, []int{1200}},
		{"every rider of the user", 0, time.Time{}, map[int]int64{5: other.ID, 60: mid.ID}, nil, nil},
		{"rider b only", b.ID, time.Time{}, map[int]int64{5: other.ID}, nil, []int{60}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.BestPowerCurve(ctx, u.ID, tc.rider, tc.since)
			if err != nil {
				t.Fatal(err)
			}
			byDur := map[int]store.BestPoint{}
			for _, p := range got {
				byDur[p.DurS] = p
			}
			for d, id := range tc.want {
				if byDur[d].RideID != id {
					t.Errorf("%ds from ride %d, want %d", d, byDur[d].RideID, id)
				}
			}
			for d, w := range tc.watts {
				if byDur[d].Watts != w {
					t.Errorf("%ds = %v W, want %v", d, byDur[d].Watts, w)
				}
			}
			for _, d := range tc.missing {
				if _, ok := byDur[d]; ok {
					t.Errorf("%ds present", d)
				}
			}
			for i := 1; i < len(got); i++ {
				if got[i].DurS <= got[i-1].DurS {
					t.Error("not ordered by duration")
				}
			}
		})
	}
}

func TestBestPowerCurveTieEarlierRide(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, u, a, _ := statsFixture(t)
	t0 := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	first := seedStatsRide(t, ctx, s, a.ID, t0, 0, []stats.Point{{DurS: 5, Watts: 500}})
	seedStatsRide(t, ctx, s, a.ID, t0.Add(time.Hour*24), 0, []stats.Point{{DurS: 5, Watts: 500}})
	got, err := s.BestPowerCurve(ctx, u.ID, a.ID, time.Time{})
	if err != nil || len(got) != 1 || got[0].RideID != first.ID || got[0].WeightKg != a.WeightKg {
		t.Fatalf("got %+v %v", got, err)
	}
}

func BenchmarkBestPowerCurve1000Rides(b *testing.B) {
	ctx := context.Background()
	s, u, a, _ := statsFixture(b)
	curve := make([]stats.Point, len(stats.Durations))
	t0 := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)
	for i := range 1000 {
		for j, d := range stats.Durations {
			curve[j] = stats.Point{DurS: d, Watts: float64(150 + (i*37+d*11)%300)}
		}
		seedStatsRide(b, ctx, s, a.ID, t0.Add(time.Duration(i)*8*time.Hour), float64(i%100), curve)
	}
	if got, err := s.BestPowerCurve(ctx, u.ID, a.ID, time.Time{}); err != nil || len(got) != len(stats.Durations) {
		b.Fatalf("warm-up: %d points, %v", len(got), err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := s.BestPowerCurve(ctx, u.ID, a.ID, time.Time{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStatsQueryPlansUseIndexes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _, _, _ := statsFixture(t)
	for _, q := range []string{
		`SELECT MIN(r.started_at) FROM rides r WHERE r.ended_at IS NOT NULL AND r.rider_id = 1 GROUP BY substr(r.started_at, 1, 13)`,
		`SELECT c.dur_s FROM rides r JOIN ride_power_curve c ON c.ride_id = r.id WHERE r.rider_id = 1 AND r.started_at >= ''`,
	} {
		rows, err := s.Read.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		rows.Close()
		if !containsAny(plan, "rides_rider_started", "rides_rider_route_started") {
			t.Errorf("no rider index in plan for %q:\n%s", q, plan)
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
