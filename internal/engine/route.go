package engine

import "math"

// Course is the route geometry a RouteMode follows (route.Profile implements
// it; the engine never imports the route package).
type Course interface {
	Length() float64           // metres
	GradeAt(d float64) float64 // fraction at distance d metres
}

// RouteConfig configures a RouteMode.
type RouteConfig struct {
	ID       int64
	Course   Course
	Mapper   Mapper
	Smoother Smoother
	LeadS    float64 // lookahead in seconds at the current speed
}

// RouteMode turns the grade ahead into a resistance level.
type RouteMode struct {
	id       int64
	course   Course
	mapper   Mapper
	smoother Smoother
	leadS    float64
	lastBias int
}

func NewRouteMode(c RouteConfig) *RouteMode {
	return &RouteMode{id: c.ID, course: c.Course, mapper: c.Mapper, smoother: c.Smoother, leadS: c.LeadS}
}

func (*RouteMode) Name() string { return "route" }

// UsesBias reports that level_delta adjusts a live bias in this mode.
func (*RouteMode) UsesBias() bool { return true }

// RouteInfo returns the route id and length in metres.
func (m *RouteMode) RouteInfo() (id int64, lengthM float64) { return m.id, m.course.Length() }

// Step reads the grade under the rider and the grade at pos + v·LeadS (clamped
// to the route end) and feeds the latter through the mapper and smoother. The
// lookahead is always v·LeadS ahead, even when the target level is far from
// the current one: the smoother moves at most MaxStep levels per MinDwell, so
// jumps of more than about six levels cannot complete within a 4 s lead.
func (m *RouteMode) Step(in ModeInput) ModeOutput {
	if in.Bias != m.lastBias {
		// A bias press moves the held level at once, bypassing dwell and
		// the dead band, so the rider feels it immediately.
		m.smoother.Nudge(in.Bias-m.lastBias, in.Elapsed)
		m.lastBias = in.Bias
	}
	l := m.course.Length()
	d := math.Min(in.DistanceM, l)
	ahead := m.course.GradeAt(math.Min(d+in.SpeedMS*m.leadS, l))
	return ModeOutput{
		Level:      m.smoother.Step(m.mapper.Target(ahead*100, float64(in.Bias)), in.Elapsed),
		Grade:      m.course.GradeAt(d),
		GradeAhead: ahead,
		Done:       in.DistanceM >= l,
	}
}
