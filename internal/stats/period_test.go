package stats_test

import (
	"math"
	"testing"
	"time"

	"github.com/metril/wattson/internal/stats"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata: %v", err)
	}
	return l
}

func TestDayOrdLocalMidnight(t *testing.T) {
	t.Parallel()
	ny := zone(t, "America/New_York")
	// 03:30 UTC on 2026-03-08 is still the 7th in New York (EST); 06:00 UTC is the 8th after the spring-forward day starts.
	a := time.Date(2026, 3, 8, 3, 30, 0, 0, time.UTC)
	b := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	if stats.DayOrd(b, ny)-stats.DayOrd(a, ny) != 1 {
		t.Fatalf("days %s %s", stats.DayString(stats.DayOrd(a, ny)), stats.DayString(stats.DayOrd(b, ny)))
	}
	if stats.DayString(stats.DayOrd(a, ny)) != "2026-03-07" {
		t.Errorf("got %s", stats.DayString(stats.DayOrd(a, ny)))
	}
}

func TestTotals(t *testing.T) {
	t.Parallel()
	ny := zone(t, "America/New_York")
	utc := time.UTC
	at := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, time.UTC) }
	b := func(ts time.Time, n int, dist float64) stats.Bucket {
		return stats.Bucket{At: ts, Rides: n, DistanceM: dist, TSS: 10 * float64(n), DurationS: 60}
	}
	tests := []struct {
		name   string
		period string
		loc    *time.Location
		now    time.Time
		in     []stats.Bucket
		want   []string // Start of each returned period
		rides  []int
	}{
		{"empty week", "week", utc, at(2026, 5, 6, 12, 0), nil, []string{"2026-05-04"}, []int{0}},
		{"week zero-filled", "week", utc, at(2026, 5, 6, 12, 0),
			[]stats.Bucket{b(at(2026, 4, 14, 8, 0), 1, 100), b(at(2026, 4, 15, 8, 0), 2, 50)},
			[]string{"2026-04-13", "2026-04-20", "2026-04-27", "2026-05-04"}, []int{3, 0, 0, 0}},
		{"year boundary iso week", "week", utc, at(2021, 1, 4, 12, 0),
			[]stats.Bucket{b(at(2020, 12, 31, 8, 0), 1, 1), b(at(2021, 1, 3, 8, 0), 1, 1)},
			[]string{"2020-12-28", "2021-01-04"}, []int{2, 0}},
		{"month tz shifts bucket", "month", ny, at(2026, 4, 15, 12, 0),
			[]stats.Bucket{b(at(2026, 4, 1, 2, 0), 1, 1)}, // still 31 Mar in New York (22:00 EDT)
			[]string{"2026-03-01", "2026-04-01"}, []int{1, 0}},
		{"year", "year", utc, at(2026, 6, 1, 0, 0),
			[]stats.Bucket{b(at(2024, 12, 31, 23, 45), 1, 1), b(at(2026, 1, 1, 0, 0), 1, 1)},
			[]string{"2024-01-01", "2025-01-01", "2026-01-01"}, []int{1, 0, 1}},
		{"dst week", "week", ny, at(2026, 3, 10, 12, 0),
			[]stats.Bucket{b(at(2026, 3, 8, 6, 0), 1, 1)}, // Sunday, spring-forward day
			[]string{"2026-03-02", "2026-03-09"}, []int{1, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stats.Totals(tc.in, tc.period, tc.loc, tc.now)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d periods %+v, want %v", len(got), got, tc.want)
			}
			for i, g := range got {
				if g.Start != tc.want[i] || g.Rides != tc.rides[i] {
					t.Errorf("[%d] = %s/%d, want %s/%d", i, g.Start, g.Rides, tc.want[i], tc.rides[i])
				}
			}
		})
	}
}

func TestTotalsSums(t *testing.T) {
	t.Parallel()
	d := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	got := stats.Totals([]stats.Bucket{
		{At: d, Rides: 1, DurationS: 10, DistanceM: 20, AscentM: 3, KJ: 4, TSS: 5},
		{At: d.Add(time.Hour), Rides: 2, DurationS: 1, DistanceM: 1, AscentM: 1, KJ: 1, TSS: 1},
	}, "month", time.UTC, d)
	if len(got) != 1 || got[0] != (stats.Total{Start: "2026-05-01", Rides: 3, DurationS: 11, DistanceM: 21, AscentM: 4, KJ: 5, TSS: 6}) {
		t.Fatalf("got %+v", got)
	}
	if stats.Totals(nil, "decade", time.UTC, d) != nil || stats.ValidPeriod("decade") {
		t.Error("decade accepted")
	}
}

func TestTotalsCap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	got := stats.Totals([]stats.Bucket{{At: now.AddDate(-5, 0, 0), Rides: 1}}, "week", time.UTC, now)
	if len(got) != 26 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestStreaks(t *testing.T) {
	t.Parallel()
	utc := time.UTC
	ord := func(y int, m time.Month, d int) int { return stats.DayOrd(time.Date(y, m, d, 12, 0, 0, 0, utc), utc) }
	today := ord(2026, 5, 6) // Wednesday
	tests := []struct {
		name string
		days []int
		want stats.StreakResult
	}{
		{"empty", nil, stats.StreakResult{}},
		{"today only", []int{today}, stats.StreakResult{1, 1, 1, 1}},
		{"through today", []int{today - 2, today - 1, today}, stats.StreakResult{3, 3, 1, 1}},
		{"through yesterday stays alive", []int{today - 2, today - 1}, stats.StreakResult{2, 2, 1, 1}},
		{"gap of two days ends it", []int{today - 3, today - 2}, stats.StreakResult{0, 2, 2, 2}},
		{"longest earlier", []int{today - 20, today - 19, today - 18, today - 17, today}, stats.StreakResult{1, 4, 1, 1}},
		{"weeks across year boundary", []int{ord(2020, 12, 31), ord(2021, 1, 4), ord(2021, 1, 11)}, stats.StreakResult{0, 1, 0, 3}},
		{"days across year boundary", []int{ord(2025, 12, 31), ord(2026, 1, 1)}, stats.StreakResult{0, 2, 0, 1}},
		{"last week keeps week streak", []int{ord(2026, 4, 28), ord(2026, 5, 1)}, stats.StreakResult{0, 1, 1, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := stats.Streaks(tc.days, today); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestStreaksDSTLocalDays(t *testing.T) {
	t.Parallel()
	ny := zone(t, "America/New_York")
	// Rides at 23:30 local on 7 and 8 March 2026 (the spring-forward night) are consecutive local days.
	r1 := time.Date(2026, 3, 7, 23, 30, 0, 0, ny)
	r2 := time.Date(2026, 3, 8, 23, 30, 0, 0, ny)
	days := []int{stats.DayOrd(r1, ny), stats.DayOrd(r2, ny)}
	if got := stats.Streaks(days, days[1]); got.CurrentDays != 2 {
		t.Errorf("got %+v", got)
	}
	if math.Abs(float64(days[1]-days[0])) != 1 {
		t.Errorf("days %v", days)
	}
}
