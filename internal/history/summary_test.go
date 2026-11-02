package history_test

import (
	"math"
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
	got := history.Summarize(ramp120())
	want := store.Summary{DurationS: 120, ElapsedS: 119, DistanceM: 1200, AvgPowerW: 200, MaxPowerW: 400, KJ: 24,
		AvgCadenceRpm: 90, AvgSpeedKmh: 36, MaxSpeedKmh: 36, AvgHRBpm: 142, MaxHRBpm: 160, AscentM: 119}
	if !near(got.NPW, 318.35, 0.01) {
		t.Errorf("NP = %v", got.NPW)
	}
	got.NPW = 0
	if !near(got.KJ, 24, 1e-9) {
		t.Errorf("KJ = %v", got.KJ)
	}
	got.KJ = 24
	if got != want {
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
	if got := history.Summarize(nil); got != (store.Summary{}) {
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
