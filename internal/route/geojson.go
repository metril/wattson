package route

import "math"

// LineString is a GeoJSON LineString geometry; coordinates are [lon, lat].
type LineString struct {
	Type        string       `json:"type"`
	Coordinates [][2]float64 `json:"coordinates"`
}

const simplifyToleranceM = 2.0

// GeoJSON returns the route simplified with Douglas-Peucker at 2 m, rounded
// to 6 decimals (about 0.1 m).
func GeoJSON(p *Profile) LineString {
	n := p.Len()
	ls := LineString{Type: "LineString", Coordinates: make([][2]float64, 0, 16)}
	if n == 0 {
		return ls
	}
	// Equirectangular projection around the mean latitude, in metres.
	var sumLat float64
	for _, v := range p.Lat {
		sumLat += v
	}
	const mPerDeg = math.Pi / 180 * earthRadiusM
	kx := mPerDeg * math.Cos(sumLat/float64(n)*math.Pi/180)
	x, y := make([]float64, n), make([]float64, n)
	for i := range x {
		x[i], y[i] = p.Lon[i]*kx, p.Lat[i]*mPerDeg
	}

	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	stack := [][2]int{{0, n - 1}}
	for len(stack) > 0 {
		lo, hi := stack[len(stack)-1][0], stack[len(stack)-1][1]
		stack = stack[:len(stack)-1]
		far, farD := -1, simplifyToleranceM
		for i := lo + 1; i < hi; i++ {
			if d := segDist(x[i], y[i], x[lo], y[lo], x[hi], y[hi]); d > farD {
				far, farD = i, d
			}
		}
		if far >= 0 {
			keep[far] = true
			stack = append(stack, [2]int{lo, far}, [2]int{far, hi})
		}
	}
	round6 := func(v float64) float64 { return math.Round(v*1e6) / 1e6 }
	for i, k := range keep {
		if k {
			ls.Coordinates = append(ls.Coordinates, [2]float64{round6(p.Lon[i]), round6(p.Lat[i])})
		}
	}
	return ls
}

// segDist is the distance from (px,py) to the segment (ax,ay)-(bx,by).
func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = min(max(((px-ax)*dx+(py-ay)*dy)/l2, 0), 1)
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}
