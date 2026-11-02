package route

import "math"

// Segment is one stretch of a synthetic route.
type Segment struct{ LengthM, GradePct float64 }

const (
	syntheticStartEle = 100.0
	metresPerDegLon   = math.Pi / 180 * earthRadiusM // matches haversine, so lengths are exact
)

// Synthetic builds points every <=10 m due east from (0, 0), starting at
// 100 m elevation, with each segment at its constant grade. Invalid segments
// are skipped and the route stops growing past MaxLengthM.
func Synthetic(segs []Segment) []Point {
	if len(segs) == 0 {
		return nil
	}
	pts := []Point{{0, 0, syntheticStartEle}}
	d, ele := 0.0, syntheticStartEle
	for _, s := range segs {
		if !(s.LengthM > 0) || math.IsInf(s.LengthM, 0) || math.IsNaN(s.GradePct) || math.IsInf(s.GradePct, 0) {
			continue
		}
		steps := int(math.Ceil(s.LengthM/StepM - 1e-9))
		step := s.LengthM / float64(steps)
		for k := 0; k < steps && d <= MaxLengthM*1.01; k++ {
			d += step
			ele += s.GradePct / 100 * step
			pts = append(pts, Point{0, d / metresPerDegLon, ele})
		}
	}
	return pts
}
