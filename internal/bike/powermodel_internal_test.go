package bike

import "testing"

// A degenerate live fit (B < 0.01) predicts via the seed, so a good sample is
// not rejected as an outlier and the fit can recover.
func TestPowerModelRepairsDegenerateFit(t *testing.T) {
	t.Parallel()
	m := &PowerModel{s: PowerModelState{A: 0.1, B: 0.005, P: [3]float64{1, 0, 1}, N: 50}}
	if !m.Observe(5, 80, 130) { // seed: 80·(0.375+0.25·5) = 130
		t.Fatal("good sample rejected by a degenerate fit's outlier gate")
	}
}
