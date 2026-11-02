package bike

import (
	"math"
	"testing"
)

// A degenerate live fit (B < 0.01) predicts via the seed, so a good sample is
// not rejected as an outlier and the fit can recover.
func TestPowerModelRepairsDegenerateFit(t *testing.T) {
	t.Parallel()
	m := &PowerModel{s: PowerModelState{A: 0.1, B: 0.005, P: [3]float64{1, 0, 1}, N: 50}}
	if !m.Observe(5, 80, 130) { // seed: 80·(0.375+0.25·5) = 130
		t.Fatal("good sample rejected by a degenerate fit's outlier gate")
	}
}

// From an invalid live fit the update must use the raw state's residual: after
// good samples from a non-seed bike the fit becomes valid and matches it.
func TestPowerModelRecoversFromDegenerateFit(t *testing.T) {
	t.Parallel()
	const a, b = 0.3, 0.35
	m := &PowerModel{s: PowerModelState{A: 0.1, B: 0.005, P: [3]float64{1, 0, 1}, N: 50}}
	for i := range 60 {
		l, rpm := 3+i%10, float64(60+(i%5)*10)
		if !m.Observe(l, rpm, rpm*(a+b*float64(l))) {
			t.Fatalf("sample %d rejected", i)
		}
	}
	if s := m.State(); s.B < pmMinB {
		t.Fatalf("fit still degenerate: %+v", s)
	}
	if p := m.Params(); math.Abs(p.A-a) > 0.02 || math.Abs(p.B-b) > 0.01 {
		t.Fatalf("params %+v, want {%v %v}", p, a, b)
	}
}
