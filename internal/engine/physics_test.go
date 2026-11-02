package engine

import (
	"math"
	"testing"
	"time"
)

func settle(v, p, grade float64) float64 {
	ph := DefaultPhysics()
	for range 600 {
		v = ph.Step(v, p, grade, time.Second)
	}
	return v
}

func TestFlat200W(t *testing.T) {
	t.Parallel()
	if k := settle(0, 200, 0) * 3.6; k < 32 || k > 34 {
		t.Fatalf("got %.2f km/h", k)
	}
}

func TestClimbSlows(t *testing.T) {
	t.Parallel()
	if k := settle(0, 200, 0.05) * 3.6; k < 15.5 || k > 17 {
		t.Fatalf("got %.2f km/h", k)
	}
}

func TestDescentCoasts(t *testing.T) {
	t.Parallel()
	if k := settle(0, 0, -0.05) * 3.6; k < 43 || k > 46 {
		t.Fatalf("got %.2f km/h", k)
	}
}

func TestCoastToStopNeverNegative(t *testing.T) {
	t.Parallel()
	ph := DefaultPhysics()
	v := 10.0
	for i := range 600 {
		v = ph.Step(v, 0, 0.05, time.Second)
		if v < 0 || math.IsNaN(v) {
			t.Fatalf("step %d: v=%v", i, v)
		}
	}
	if v != 0 {
		t.Fatalf("v=%v, want 0", v)
	}
}

func TestStartFromRest(t *testing.T) {
	t.Parallel()
	if v := DefaultPhysics().Step(0, 200, 0, time.Second); v <= 0 || v >= 5 {
		t.Fatalf("v=%v", v)
	}
}
