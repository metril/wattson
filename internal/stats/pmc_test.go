package stats_test

import (
	"math"
	"testing"

	"github.com/metril/wattson/internal/stats"
)

func TestPMCConstantLoadConverges(t *testing.T) {
	t.Parallel()
	load := map[int]float64{}
	for d := 0; d < 400; d++ {
		load[d] = 50
	}
	p := stats.PMC(load, 0, 399)
	if len(p) != 400 {
		t.Fatalf("len = %d", len(p))
	}
	last := p[399]
	if math.Abs(last.CTL-50) > 0.1 || math.Abs(last.ATL-50) > 1e-6 || math.Abs(last.TSB) > 0.1 {
		t.Errorf("last = %+v", last)
	}
	if p[0].CTL <= 0 || p[0].ATL <= p[0].CTL {
		t.Errorf("first day = %+v; ATL should react faster", p[0])
	}
}

func TestPMCSpikeDecay(t *testing.T) {
	t.Parallel()
	p := stats.PMC(map[int]float64{0: 300}, 0, 60)
	if p[0].TSS != 300 || p[0].TSB >= 0 {
		t.Errorf("spike day = %+v", p[0])
	}
	// ATL decays by e^-1 per 7 days, CTL by e^-1 per 42 days (after the spike day).
	if r := p[8].ATL / p[1].ATL; math.Abs(r-math.Exp(-1)) > 1e-9 {
		t.Errorf("ATL ratio = %v", r)
	}
	if r := p[43].CTL / p[1].CTL; math.Abs(r-math.Exp(-1)) > 1e-9 {
		t.Errorf("CTL ratio = %v", r)
	}
	if p[60].TSB <= 0 {
		t.Errorf("form should turn positive after rest, got %v", p[60].TSB)
	}
}

func TestPMCRange(t *testing.T) {
	t.Parallel()
	if stats.PMC(nil, 5, 4) != nil {
		t.Error("reversed range")
	}
	p := stats.PMC(nil, 3, 5)
	if len(p) != 3 || p[0].Day != 3 || p[2].CTL != 0 {
		t.Errorf("empty history = %+v", p)
	}
}

func TestEFTP(t *testing.T) {
	t.Parallel()
	if _, ok := stats.EFTP([]stats.Point{{DurS: 60, Watts: 400}}); ok {
		t.Error("no 20 min point")
	}
	v, ok := stats.EFTP([]stats.Point{{DurS: 1200, Watts: 200}})
	if !ok || math.Abs(v-190) > 1e-9 {
		t.Errorf("eftp = %v %v", v, ok)
	}
}
