package history_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/store"
)

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

func ramp120() []store.Sample {
	out := make([]store.Sample, 120)
	for i := range out {
		s := store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(10 * (i + 1)), SpeedKmh: 36, Level: 5,
			Lat: math.NaN(), Lon: math.NaN(), EleM: float64(100 + i)}
		if i >= 60 {
			s.PowerW, s.CadenceRpm = 400, 90
		}
		switch {
		case i < 10:
		case i < 60:
			s.HRBpm = 120
		default:
			s.HRBpm = 160
		}
		out[i] = s
	}
	return out
}

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestSummarizeRamp(t *testing.T) {
	t.Parallel()
	got := history.Summarize(ramp120(), history.Params{})
	want := store.Summary{DurationS: 120, ElapsedS: 119, DistanceM: 1200, AvgPowerW: 200, MaxPowerW: 400, KJ: 24,
		AvgCadenceRpm: 90, AvgSpeedKmh: 36, MaxSpeedKmh: 36, AvgHRBpm: 142, MaxHRBpm: 160, AscentM: 119, MaxCadenceRpm: 90}
	if !near(got.NPW, 318.35, 0.01) {
		t.Errorf("NP = %v", got.NPW)
	}
	got.NPW = 0
	if !near(got.KJ, 24, 1e-9) {
		t.Errorf("KJ = %v", got.KJ)
	}
	got.KJ = 24
	if got.VI == nil || got.StatsVersion != store.StatsVersion || len(got.Curve) == 0 {
		t.Errorf("VI/version/curve missing: %+v", got)
	}
	got.VI, got.EF, got.VAM, got.StatsVersion, got.Curve = nil, nil, nil, 0, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary = %+v\nwant     %+v", got, want)
	}
}

func TestNPShortRide(t *testing.T) {
	t.Parallel()
	w := make([]float64, 20)
	for i := range w {
		w[i] = 250
	}
	if got := history.NormalizedPower(w); got != 250 {
		t.Errorf("NP = %v", got)
	}
}

func TestNPConstant(t *testing.T) {
	t.Parallel()
	w := make([]float64, 120)
	for i := range w {
		w[i] = 250
	}
	if got := history.NormalizedPower(w); !near(got, 250, 1e-9) {
		t.Errorf("NP = %v", got)
	}
}

func TestAscentVirtualFromGrade(t *testing.T) {
	t.Parallel()
	ss := make([]store.Sample, 120)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, DistanceM: float64(10 * i), GradePct: 5, EleM: math.NaN(), Lat: math.NaN(), Lon: math.NaN()}
	}
	if got := history.Ascent(ss); !near(got, 59, 1e-9) {
		t.Errorf("ascent = %v, want 59", got)
	}
}

func TestAscentDeadBand(t *testing.T) {
	t.Parallel()
	ss := make([]store.Sample, 50)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, EleM: 100 + 0.6*float64(i%2)}
	}
	if got := history.Ascent(ss); got != 0 {
		t.Errorf("ascent = %v, want 0", got)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	t.Parallel()
	if got := history.Summarize(nil, history.Params{}); !reflect.DeepEqual(got, store.Summary{StatsVersion: store.StatsVersion}) {
		t.Errorf("empty = %+v", got)
	}
}

func TestDownsample(t *testing.T) {
	t.Parallel()
	mk := func(n int) []store.Sample {
		out := make([]store.Sample, n)
		for i := range out {
			out[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), PowerW: float64(i),
				Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
		}
		return out
	}
	got := history.Downsample(mk(3600), 600)
	if len(got) != 600 || got[0].PowerW != 2.5 || got[0].Seq != 0 || got[1].Seq != 6 || !got[1].At.Equal(t0.Add(6*time.Second)) {
		t.Fatalf("3600 -> %d, [0]=%+v [1].Seq=%d", len(got), got[0], got[1].Seq)
	}
	for _, p := range got {
		if !math.IsNaN(p.Lat) {
			t.Fatal("Lat should stay NaN")
		}
	}
	if got := history.Downsample(mk(601), 600); len(got) != 301 {
		t.Errorf("601 -> %d, want 301", len(got))
	}
	in := mk(500)
	out := history.Downsample(in, 600)
	if len(out) != 500 {
		t.Fatalf("500 -> %d", len(out))
	}
	out[0].PowerW = 99
	if in[0].PowerW == 99 {
		t.Error("Downsample must return a copy")
	}
}

func TestDownsampleBucketMeans(t *testing.T) {
	t.Parallel()
	ss := []store.Sample{
		{Seq: 0, HRBpm: 0, Level: 4, Lat: 10, Lon: math.NaN(), EleM: math.NaN()},
		{Seq: 1, HRBpm: 150, Level: 5, Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()},
		{Seq: 2, HRBpm: 0, Level: 5, Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()},
	}
	got := history.Downsample(ss, 2)
	if len(got) != 2 || got[0].HRBpm != 150 || got[0].Level != 5 || got[0].Lat != 10 || got[1].HRBpm != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestDownsampleEndsAtTrueEnd(t *testing.T) {
	t.Parallel()
	ss := ramp120()
	got := history.Downsample(ss, 10)
	if len(got) != 10 {
		t.Fatalf("len = %d", len(got))
	}
	last := got[len(got)-1]
	if last.DistanceM != 1200 || last.ElapsedS != 120 {
		t.Errorf("last = dist %v elapsed %v, want 1200 / 120", last.DistanceM, last.ElapsedS)
	}
}

func steady(n int, w float64, hr int) []store.Sample {
	out := make([]store.Sample, n)
	for i := range out {
		out[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(10 * (i + 1)), SpeedKmh: 36, PowerW: w, CadenceRpm: 80, HRBpm: hr,
			Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
	}
	return out
}

func TestSummarizeMetrics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		ss     []store.Sample
		p      history.Params
		tss    float64 // 0 = nil expected
		pz, hz int     // expected zone slice lengths
		dec    bool
		done   bool
	}{
		{"no ftp no hr", steady(60, 200, 0), history.Params{}, 0, 0, 0, false, false},
		{"hour at ftp", steady(3600, 200, 140), history.Params{FTPW: 200, MaxHRBpm: 190}, 100, 7, 5, true, false},
		{"short with hr", steady(600, 200, 140), history.Params{FTPW: 200, MaxHRBpm: 190}, 600.0 / 3600 * 100, 7, 5, false, false},
		{"route done", steady(100, 100, 0), history.Params{RouteDistanceM: 1000}, 0, 0, 0, false, true},
		{"route partial", steady(100, 100, 0), history.Params{RouteDistanceM: 2000}, 0, 0, 0, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := history.Summarize(tc.ss, tc.p)
			if (got.TSS != nil) != (tc.tss != 0) || (got.TSS != nil && !near(*got.TSS, tc.tss, 1e-6)) {
				t.Errorf("TSS = %v, want %v", got.TSS, tc.tss)
			}
			if len(got.PowerZones) != tc.pz || len(got.HRZones) != tc.hz {
				t.Errorf("zones = %d/%d, want %d/%d", len(got.PowerZones), len(got.HRZones), tc.pz, tc.hz)
			}
			if (got.DecouplingPct != nil) != tc.dec {
				t.Errorf("decoupling = %v, want present=%v", got.DecouplingPct, tc.dec)
			}
			if got.Completed != tc.done {
				t.Errorf("completed = %v", got.Completed)
			}
			if got.IntensityFactor != nil && !near(*got.IntensityFactor, 1, 1e-9) {
				t.Errorf("IF = %v", *got.IntensityFactor)
			}
			if tc.pz > 0 && got.PowerZones[3] != float64(len(tc.ss)) { // 200 W at FTP 200 is zone 4
				t.Errorf("power zones = %v", got.PowerZones)
			}
		})
	}
}

func BenchmarkSummarize2h(b *testing.B) {
	ss := steady(7200, 180, 140)
	for i := range ss {
		ss[i].PowerW = 150 + float64(i%100)
	}
	p := history.Params{FTPW: 250, MaxHRBpm: 190}
	b.ReportAllocs()
	for b.Loop() {
		history.Summarize(ss, p)
	}
}
