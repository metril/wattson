package stats

import (
	"testing"
	"time"
)

func TestSummarizeRoute(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		in        []Attempt
		completed int
		best      float64
		pr        int64
		avg       float64
	}{
		{"empty", nil, 0, 0, 0, 0},
		{"none completed", []Attempt{{RideID: 1, ElapsedS: 50}}, 0, 0, 0, 0},
		{"pr and tie", []Attempt{
			{RideID: 1, StartedAt: t0, ElapsedS: 120, Completed: true, AvgHRBpm: 140},
			{RideID: 2, StartedAt: t0, ElapsedS: 100, Completed: true},
			{RideID: 3, StartedAt: t0, ElapsedS: 100, Completed: true},
			{RideID: 4, ElapsedS: 10},
		}, 3, 100, 2, 320.0 / 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := SummarizeRoute(tc.in)
			if s.Attempts != len(tc.in) || s.Completed != tc.completed || s.PRRideID != tc.pr {
				t.Fatalf("got %+v", s)
			}
			if tc.completed == 0 {
				if s.BestS != nil || s.PRDate != nil {
					t.Fatalf("expected nil: %+v", s)
				}
				return
			}
			if *s.BestS != tc.best || *s.AvgS != tc.avg || *s.LastS != 100 {
				t.Fatalf("times %+v", s)
			}
			for i, a := range tc.in {
				if s.IsPR[i] != (a.RideID == tc.pr) {
					t.Fatalf("IsPR[%d]", i)
				}
			}
			if *s.AvgHRBpm != 140 {
				t.Fatalf("hr %v", *s.AvgHRBpm)
			}
		})
	}
}

func TestGhostTrack(t *testing.T) {
	t.Parallel()
	if GhostTrack(nil, nil) != nil {
		t.Fatal("nil")
	}
	d := []float64{0, 4, 8, 12, 20, 22, 23}
	e := []float64{0, 1, 2, 3, 5, 6, 7}
	got := GhostTrack(d, e)
	want := [][2]float64{{0, 0}, {12, 3}, {22, 6}, {23, 7}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
