package route

import (
	"errors"
	"math"
	"slices"
)

const (
	StepM        = 10.0      // resampling interval, metres
	MedianWindow = 5         // elevation despike window, samples
	SGWindow     = 11        // Savitzky-Golay window, samples
	GradeWindowM = 80.0      // grade is a central difference over this span
	MaxGradePct  = 25.0      // grades are clamped to +-MaxGradePct
	MinLengthM   = 50.0      // shorter routes are rejected
	MaxLengthM   = 500_000.0 // longer routes are rejected

	earthRadiusM = 6371008.8
	minSpacingM  = 0.5 // consecutive points closer than this are dropped
	deadBandM    = 1.0 // elevation hysteresis for ascent/descent
)

var (
	ErrTooShort    = errors.New("route: route is shorter than 50 m")
	ErrTooLong     = errors.New("route: route is longer than 500 km")
	ErrNoElevation = errors.New("route: more than 10% of points have no elevation")
)

// Profile is a route resampled every StepM metres.
type Profile struct {
	StepM, LengthM       float64
	Lat, Lon, Ele, Grade []float64 // sample i at distance min(i*StepM, LengthM); Grade in percent
}

// Len is the number of samples.
func (p *Profile) Len() int { return len(p.Lat) }

// DistAt is the distance along the route of sample i, in metres.
func (p *Profile) DistAt(i int) float64 { return min(float64(i)*p.StepM, p.LengthM) }

// Length implements engine.Course.
func (p *Profile) Length() float64 { return p.LengthM }

// locate returns the interval index i and fraction t in [0,1] for distance d.
func (p *Profile) locate(d float64) (int, float64) {
	n := p.Len()
	if !(d > 0) { // also NaN
		return 0, 0
	}
	if d >= p.LengthM {
		return n - 2, 1
	}
	i := min(int(d/p.StepM), n-2)
	d0, d1 := p.DistAt(i), p.DistAt(i+1)
	if d1 <= d0 {
		return i, 0
	}
	return i, min(max((d-d0)/(d1-d0), 0), 1)
}

// GradeAt implements engine.Course: the grade as a fraction at distance d
// metres, linearly interpolated and clamped to the route.
func (p *Profile) GradeAt(d float64) float64 {
	n := p.Len()
	switch {
	case n == 0:
		return 0
	case !(d > 0) || n == 1:
		return p.Grade[0] / 100
	case d >= p.LengthM:
		return p.Grade[n-1] / 100
	}
	i, t := p.locate(d)
	return (p.Grade[i] + (p.Grade[i+1]-p.Grade[i])*t) / 100
}

// PosAt returns the position at distance d metres, clamped to the route.
func (p *Profile) PosAt(d float64) (lat, lon float64) {
	n := p.Len()
	switch {
	case n == 0:
		return 0, 0
	case !(d > 0) || n == 1:
		return p.Lat[0], p.Lon[0]
	case d >= p.LengthM:
		return p.Lat[n-1], p.Lon[n-1]
	}
	i, t := p.locate(d)
	return p.Lat[i] + (p.Lat[i+1]-p.Lat[i])*t, lerpLon(p.Lon[i], p.Lon[i+1], t)
}

// Stats summarises a Profile.
type Stats struct {
	DistanceM, AscentM, DescentM, MinGradePct, MaxGradePct float64
	BBox                                                   [4]float64 // W S E N, degrees
}

// Stats computes totals from the smoothed elevation with a 1 m dead band.
func (p *Profile) Stats() Stats {
	s := Stats{DistanceM: p.LengthM}
	if p.Len() == 0 {
		return s
	}
	s.MinGradePct, s.MaxGradePct = slices.Min(p.Grade), slices.Max(p.Grade)
	s.BBox = [4]float64{slices.Min(p.Lon), slices.Min(p.Lat), slices.Max(p.Lon), slices.Max(p.Lat)}
	// Zig-zag: a reversal counts only once it exceeds the dead band.
	pivot, ext, dir := p.Ele[0], p.Ele[0], 0
	for _, e := range p.Ele[1:] {
		switch dir {
		case 0:
			if e-pivot >= deadBandM {
				dir, s.AscentM, ext = 1, s.AscentM+e-pivot, e
			} else if pivot-e >= deadBandM {
				dir, s.DescentM, ext = -1, s.DescentM+pivot-e, e
			}
		case 1:
			if e > ext {
				s.AscentM += e - ext
				ext = e
			} else if ext-e >= deadBandM {
				dir, s.DescentM, ext = -1, s.DescentM+ext-e, e
			}
		default:
			if e < ext {
				s.DescentM += ext - e
				ext = e
			} else if e-ext >= deadBandM {
				dir, s.AscentM, ext = 1, s.AscentM+e-ext, e
			}
		}
	}
	return s
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// lerpLon interpolates longitudes across the antimeridian.
func lerpLon(a, b, t float64) float64 {
	d := b - a
	if d > 180 {
		d -= 360
	} else if d < -180 {
		d += 360
	}
	v := a + d*t
	if v > 180 {
		v -= 360
	} else if v < -180 {
		v += 360
	}
	return v
}

// sgCoef are the quadratic, 11-point Savitzky-Golay smoothing weights (x 1/429).
var sgCoef = [SGWindow]float64{-36, 9, 44, 69, 84, 89, 84, 69, 44, 9, -36}

// Process cleans, resamples, smooths and grades raw points.
func Process(pts []Point) (*Profile, error) {
	// 1. Drop near-duplicates; cumulative haversine distance.
	kept := make([]Point, 0, len(pts))
	dist := make([]float64, 0, len(pts))
	total := 0.0
	for _, p := range pts {
		if n := len(kept); n > 0 {
			dd := haversine(kept[n-1].Lat, kept[n-1].Lon, p.Lat, p.Lon)
			if !(dd >= minSpacingM) {
				continue
			}
			total += dd
		}
		kept = append(kept, p)
		dist = append(dist, total)
	}
	switch {
	case len(kept) < 2 || total < MinLengthM:
		return nil, ErrTooShort
	case total > MaxLengthM:
		return nil, ErrTooLong
	}
	length := total

	// 2. Elevation gaps.
	if err := fillElevation(kept, dist); err != nil {
		return nil, err
	}

	// 3. Resample.
	n := int(length/StepM) + 1
	if length-float64(n-1)*StepM > 0.01 {
		n++
	}
	p := &Profile{
		StepM: StepM, LengthM: length,
		Lat: make([]float64, n), Lon: make([]float64, n), Ele: make([]float64, n), Grade: make([]float64, n),
	}
	j := 0
	for i := 0; i < n; i++ {
		s := min(float64(i)*StepM, length)
		for j < len(kept)-2 && dist[j+1] < s {
			j++
		}
		a, b := kept[j], kept[j+1]
		t := min(max((s-dist[j])/(dist[j+1]-dist[j]), 0), 1)
		p.Lat[i] = a.Lat + (b.Lat-a.Lat)*t
		p.Lon[i] = lerpLon(a.Lon, b.Lon, t)
		p.Ele[i] = a.Ele + (b.Ele-a.Ele)*t
	}

	// 4. Despike, then smooth.
	p.Ele = savitzkyGolay(medianFilter(p.Ele, MedianWindow))

	// 5. Grade: central difference over GradeWindowM, clamped.
	h := int(math.Round(GradeWindowM / 2 / StepM))
	for i := 0; i < n; i++ {
		k, jj := max(0, i-h), min(n-1, i+h)
		g := (p.Ele[jj] - p.Ele[k]) / (p.DistAt(jj) - p.DistAt(k)) * 100
		p.Grade[i] = min(max(g, -MaxGradePct), MaxGradePct)
	}
	return p, nil
}

// fillElevation interpolates NaN elevations over distance in place.
func fillElevation(pts []Point, dist []float64) error {
	missing := 0
	for i := range pts {
		if math.IsNaN(pts[i].Ele) || math.IsInf(pts[i].Ele, 0) {
			pts[i].Ele = math.NaN()
			missing++
		}
	}
	if missing == 0 {
		return nil
	}
	if float64(missing) > 0.1*float64(len(pts)) {
		return ErrNoElevation
	}
	prev := -1 // last known index
	for i := range pts {
		if !math.IsNaN(pts[i].Ele) {
			if prev >= 0 && i-prev > 1 {
				for k := prev + 1; k < i; k++ {
					t := (dist[k] - dist[prev]) / (dist[i] - dist[prev])
					pts[k].Ele = pts[prev].Ele + (pts[i].Ele-pts[prev].Ele)*t
				}
			} else if prev < 0 {
				for k := 0; k < i; k++ {
					pts[k].Ele = pts[i].Ele
				}
			}
			prev = i
		}
	}
	for k := prev + 1; k < len(pts); k++ {
		pts[k].Ele = pts[prev].Ele
	}
	return nil
}

// medianFilter applies a running median; the window is clipped (shrinks) at
// the edges (even-sized clipped windows take the upper middle value).
func medianFilter(x []float64, window int) []float64 {
	out := make([]float64, len(x))
	var buf [MedianWindow]float64
	for i := range x {
		w := append(buf[:0], x[max(0, i-window/2):min(len(x), i+window/2+1)]...)
		slices.Sort(w)
		out[i] = w[len(w)/2]
	}
	return out
}

// savitzkyGolay smooths with the quadratic 11-point filter, padding both ends
// by odd reflection so linear trends survive.
func savitzkyGolay(x []float64) []float64 {
	n := len(x)
	const half = SGWindow / 2
	at := func(i int) float64 {
		switch {
		case i < 0:
			return 2*x[0] - x[min(-i, n-1)]
		case i >= n:
			return 2*x[n-1] - x[max(n-1-(i-(n-1)), 0)]
		}
		return x[i]
	}
	out := make([]float64, n)
	for i := range x {
		var s float64
		if i >= half && i < n-half {
			for k, c := range sgCoef {
				s += c * x[i-half+k]
			}
		} else {
			for k, c := range sgCoef {
				s += c * at(i-half+k)
			}
		}
		out[i] = s / 429
	}
	return out
}
