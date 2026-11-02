package engine

import "math"

// Mapper converts a route grade into a target resistance level.
type Mapper struct {
	Gain          float64 // levels per percent of climb
	Offset        float64 // level on flat ground
	DownhillScale float64 // multiplies negative grades (0 = descents ignored)
	Min, Max      int     // rider range; Min is floored at 1 (never level 0)
}

// Target = clamp(Offset + bias + g·Gain, max(1,Min), Max) where g is gradePct
// if >= 0, else gradePct·DownhillScale. A NaN input yields the lower bound.
func (m Mapper) Target(gradePct, bias float64) float64 {
	lo := float64(max(1, m.Min))
	hi := math.Max(lo, float64(m.Max))
	g := gradePct
	if g < 0 {
		g *= m.DownhillScale
	}
	v := m.Offset + bias + g*m.Gain
	if math.IsNaN(v) {
		return lo
	}
	return math.Max(lo, math.Min(hi, v))
}
