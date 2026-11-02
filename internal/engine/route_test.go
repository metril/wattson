package engine

import (
	"testing"
	"time"
)

// fakeCourse is flat below 1000 m and a 6 % climb above.
type fakeCourse struct{ L float64 }

func (c fakeCourse) Length() float64 { return c.L }
func (c fakeCourse) GradeAt(d float64) float64 {
	if d < 1000 {
		return 0
	}
	return 0.06
}

func newTestRoute(l float64) *RouteMode {
	return NewRouteMode(RouteConfig{
		ID: 7, Course: fakeCourse{L: l},
		Mapper:   Mapper{Gain: 1, Offset: 5, DownhillScale: 0.5, Min: 1, Max: 16},
		Smoother: DefaultSmoother(1, 16), LeadS: 4,
	})
}

func TestRouteModeLookahead(t *testing.T) {
	t.Parallel()
	out := newTestRoute(2000).Step(ModeInput{DistanceM: 970, SpeedMS: 10})
	if out.Grade != 0 || out.GradeAhead != 0.06 || out.Level != 11 {
		t.Fatalf("out %+v", out)
	}
	out = newTestRoute(2000).Step(ModeInput{DistanceM: 900, SpeedMS: 10})
	if out.GradeAhead != 0 || out.Level != 5 {
		t.Fatalf("out %+v", out)
	}
}

func TestRouteModeDone(t *testing.T) {
	t.Parallel()
	m := newTestRoute(2000)
	if !m.Step(ModeInput{DistanceM: 2000}).Done {
		t.Fatal("not done at 2000")
	}
	m = newTestRoute(2000)
	out := m.Step(ModeInput{DistanceM: 1999, SpeedMS: 10, Elapsed: time.Second})
	if out.Done || out.GradeAhead != 0.06 {
		t.Fatalf("out %+v", out)
	}
	// Lookahead past the end clamps to the final grade (GradeAt(2000)).
	m = NewRouteMode(RouteConfig{
		Course: fakeCourse{L: 1000}, Mapper: Mapper{Gain: 1, Offset: 5, Min: 1, Max: 16},
		Smoother: DefaultSmoother(1, 16), LeadS: 4,
	})
	if out := m.Step(ModeInput{DistanceM: 990, SpeedMS: 10}); out.GradeAhead != 0.06 {
		t.Fatalf("out %+v", out)
	}
}

func TestRouteModeBias(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ bias, want int }{{2, 7}, {-10, 1}, {0, 5}} {
		if got := newTestRoute(2000).Step(ModeInput{DistanceM: 10, Bias: c.bias}).Level; got != c.want {
			t.Errorf("bias %d: level %d, want %d", c.bias, got, c.want)
		}
	}
}

func TestRouteModeInfo(t *testing.T) {
	t.Parallel()
	m := newTestRoute(2000)
	id, l := m.RouteInfo()
	if m.Name() != "route" || !m.UsesBias() || id != 7 || l != 2000 {
		t.Fatalf("%s %v %d %v", m.Name(), m.UsesBias(), id, l)
	}
}
