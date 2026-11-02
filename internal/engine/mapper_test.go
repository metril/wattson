package engine

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestMapperTarget(t *testing.T) {
	t.Parallel()
	m := Mapper{Gain: 1, Offset: 5, DownhillScale: 0.5, Min: 1, Max: 16}
	for _, c := range []struct{ g, bias, want float64 }{
		{0, 0, 5}, {6, 0, 11}, {-4, 0, 3}, {-20, 0, 1}, {25, 0, 16}, {3, 2, 10},
	} {
		if got := m.Target(c.g, c.bias); got != c.want {
			t.Errorf("Target(%v,%v)=%v want %v", c.g, c.bias, got, c.want)
		}
	}
}

func TestMapperNeverBelowOne(t *testing.T) {
	t.Parallel()
	m := Mapper{Gain: 1, Offset: 5, DownhillScale: 0.5, Min: 0, Max: 16}
	if got := m.Target(-30, -10); got != 1 {
		t.Fatalf("got %v", got)
	}
	if got := m.Target(math.NaN(), 0); got != 1 {
		t.Fatalf("NaN: got %v", got)
	}
	if got := (Mapper{Gain: 1, Offset: 5, Min: -3, Max: 0}).Target(0, 0); got < 1 {
		t.Fatalf("degenerate range: got %v", got)
	}
}

func TestMapperGolden(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/mapper_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Gain, Offset  float64
		DownhillScale float64 `json:"downhill_scale"`
		Min, Max      int
		GradePct      float64 `json:"grade_pct"`
		Bias, Want    float64
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 12 {
		t.Fatalf("only %d golden cases", len(cases))
	}
	for i, c := range cases {
		m := Mapper{Gain: c.Gain, Offset: c.Offset, DownhillScale: c.DownhillScale, Min: c.Min, Max: c.Max}
		if got := m.Target(c.GradePct, c.Bias); math.Abs(got-c.Want) > 1e-9 {
			t.Errorf("case %d: got %v want %v", i, got, c.Want)
		}
	}
}
