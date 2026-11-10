package stats_test

import (
	"math"
	"testing"

	"github.com/metril/wattson/internal/stats"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func prefixOf(v []float64) []float64 { return stats.Prefix(make([]float64, len(v)+1), v) }

func constant(n int, w float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = w
	}
	return v
}

func TestPowerCurve(t *testing.T) {
	t.Parallel()
	ramp := make([]float64, 100)
	for i := range ramp {
		ramp[i] = float64(i)
	}
	tests := []struct {
		name  string
		in    []float64
		count int
		dur   int
		watts float64
	}{
		{"empty", nil, 0, 0, 0},
		{"constant 10s", constant(10, 200), 8, 10, 200},
		{"ramp best is last window", ramp, 18, 60, 69.5},
		{"two hours", constant(7200, 100), len(stats.Durations), 7200, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := stats.PowerCurve(prefixOf(tc.in))
			if len(c) != tc.count {
				t.Fatalf("len = %d, want %d", len(c), tc.count)
			}
			if tc.count == 0 {
				return
			}
			var found bool
			for _, p := range c {
				if p.DurS == tc.dur {
					found = true
					if !near(p.Watts, tc.watts) {
						t.Errorf("%d s = %v, want %v", p.DurS, p.Watts, tc.watts)
					}
				}
			}
			if !found {
				t.Errorf("duration %d missing", tc.dur)
			}
		})
	}
}

func TestNP(t *testing.T) {
	t.Parallel()
	if got := stats.NP(prefixOf(constant(120, 250))); !near(got, 250) {
		t.Errorf("constant NP = %v", got)
	}
	if got := stats.NP(prefixOf(constant(10, 180))); !near(got, 180) {
		t.Errorf("short NP = %v", got)
	}
	if got := stats.NP(prefixOf(nil)); got != 0 {
		t.Errorf("empty NP = %v", got)
	}
}

func TestZoneOf(t *testing.T) {
	t.Parallel()
	b := stats.PowerBounds(200)
	tests := []struct {
		w    float64
		zone int
	}{{0, 0}, {109.9, 0}, {111, 1}, {149, 1}, {150, 2}, {180, 3}, {210, 4}, {239, 4}, {240, 5}, {300, 6}, {900, 6}}
	for _, tc := range tests {
		if got := stats.ZoneOf(tc.w, b[:]); got != tc.zone {
			t.Errorf("ZoneOf(%v) = %d, want %d", tc.w, got, tc.zone)
		}
	}
	h := stats.HRBounds(200)
	if got := stats.ZoneOf(185, h[:]); got != 4 {
		t.Errorf("hr zone = %d", got)
	}
}

func TestScores(t *testing.T) {
	t.Parallel()
	if v, ok := stats.TSS(3600, 200, 200); !ok || !near(v, 100) {
		t.Errorf("TSS = %v %v", v, ok)
	}
	if _, ok := stats.TSS(3600, 200, 0); ok {
		t.Error("TSS with FTP 0 should be unknown")
	}
	if _, ok := stats.IF(100, 0); ok {
		t.Error("IF with FTP 0")
	}
	if v, ok := stats.VI(220, 200); !ok || !near(v, 1.1) {
		t.Errorf("VI = %v", v)
	}
	if _, ok := stats.VI(1, 0); ok {
		t.Error("VI with no power")
	}
	if v, ok := stats.EF(200, 100); !ok || !near(v, 2) {
		t.Errorf("EF = %v", v)
	}
	if _, ok := stats.EF(200, 0); ok {
		t.Error("EF without HR")
	}
}

func TestDecoupling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
		args [4]float64
		want float64
		ok   bool
	}{
		{"short ride", 600, [4]float64{200, 140, 200, 150}, 0, false},
		{"no hr", 3600, [4]float64{200, 0, 200, 0}, 0, false},
		{"flat", 3600, [4]float64{200, 140, 200, 140}, 0, true},
		{"drift", 3600, [4]float64{200, 100, 200, 110}, 100.0 / 11, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := stats.Decoupling(tc.n, tc.args[0], tc.args[1], tc.args[2], tc.args[3])
			if ok != tc.ok || (ok && !near(v, tc.want)) {
				t.Errorf("got %v %v, want %v %v", v, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestVAMAndCompleted(t *testing.T) {
	t.Parallel()
	if v, ok := stats.VAM(300, 1800); !ok || !near(v, 600) {
		t.Errorf("VAM = %v", v)
	}
	if _, ok := stats.VAM(0, 1800); ok {
		t.Error("flat VAM")
	}
	for _, tc := range []struct {
		d, r float64
		want bool
	}{{9800, 10000, true}, {9799, 10000, false}, {5000, 0, false}, {12000, 10000, true}} {
		if got := stats.Completed(tc.d, tc.r); got != tc.want {
			t.Errorf("Completed(%v,%v) = %v", tc.d, tc.r, got)
		}
	}
}
