package bike_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
)

func TestPowerModelSeedMatchesSim(t *testing.T) {
	t.Parallel()
	m := bike.NewPowerModel(bike.SeedState())
	for l := 1; l <= 16; l++ {
		for _, rpm := range []float64{60, 80, 100} {
			if d := math.Abs(m.Power(l, rpm) - sim.Power(l, rpm)); d > 1e-9 {
				t.Fatalf("L%d rpm %v: model %v sim %v", l, rpm, m.Power(l, rpm), sim.Power(l, rpm))
			}
		}
	}
}

func TestPowerModelLearnsSim(t *testing.T) {
	t.Parallel()
	m := bike.NewPowerModel(bike.PowerModelState{A: 0.2, B: 0.4, P: [3]float64{1, 0, 1}})
	for range 2 {
		for l := 1; l <= 16; l++ {
			for _, rpm := range []float64{60, 70, 80, 90, 100} {
				if !m.Observe(l, rpm, sim.Power(l, rpm)) {
					t.Fatalf("rejected L%d rpm %v", l, rpm)
				}
			}
		}
	}
	s := m.State()
	if math.Abs(s.A-0.375) > 1e-3 || math.Abs(s.B-0.25) > 1e-3 || s.N != 160 {
		t.Fatalf("state %+v", s)
	}
}

func TestPowerModelInverse(t *testing.T) {
	t.Parallel()
	m := bike.NewPowerModel(bike.SeedState())
	for _, c := range []struct{ w, rpm, want float64 }{{150, 80, 6}, {250, 80, 11}, {200, 80, 8.5}} {
		if got := m.LevelFor(c.w, c.rpm); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("LevelFor(%v,%v) = %v, want %v", c.w, c.rpm, got, c.want)
		}
	}
	if got := m.Slope(80); math.Abs(got-20) > 1e-9 {
		t.Fatalf("Slope = %v", got)
	}
	if !math.IsNaN(m.LevelFor(100, 0)) {
		t.Fatal("rpm 0 should be NaN")
	}
}

func TestPowerModelRejectsGarbage(t *testing.T) {
	t.Parallel()
	bad := []struct {
		l         int
		rpm, w    float64
		priorGood int
	}{
		{5, 80, 0, 0}, {5, 80, math.NaN(), 0}, {5, 80, 5000, 0}, {5, 10, 100, 0}, {0, 80, 100, 0},
		{5, 200, 100, 0}, {5, math.Inf(1), 100, 0},
		{5, 80, 400, 20}, // outlier; truth is 130
	}
	for _, c := range bad {
		m := bike.NewPowerModel(bike.SeedState())
		for i := range c.priorGood {
			l, rpm := 3+i%6, float64(60+(i%5)*10)
			if !m.Observe(l, rpm, sim.Power(l, rpm)) {
				t.Fatal("good sample rejected")
			}
		}
		before := m.State()
		if m.Observe(c.l, c.rpm, c.w) {
			t.Fatalf("accepted %+v", c)
		}
		if after := m.State(); after != before {
			t.Fatalf("state changed: %+v -> %+v", before, after)
		}
	}
}

func TestPowerModelInvalidFallsBack(t *testing.T) {
	t.Parallel()
	for _, s := range []bike.PowerModelState{{A: 1, B: -0.5, P: [3]float64{1, 0, 1}}, {A: math.NaN()}, {}} {
		if got := bike.NewPowerModel(s).Params(); got != bike.Seed {
			t.Fatalf("%+v -> %+v", s, got)
		}
	}
}

func TestPowerModelStateJSON(t *testing.T) {
	t.Parallel()
	in := bike.PowerModelState{A: 0.4, B: 0.3, P: [3]float64{1, 0.1, 2}, N: 7}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b", "p", "n"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing key %q in %s", k, b)
		}
	}
	if len(raw) != 4 {
		t.Fatalf("keys: %s", b)
	}
	var out bike.PowerModelState
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("round trip %+v err %v", out, err)
	}
}
