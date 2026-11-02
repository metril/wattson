// Package route imports GPX, TCX and FIT files and turns them into a
// smoothed, evenly resampled elevation Profile.
package route

import "math"

// Point is one raw track point. Ele is math.NaN() when the file has none.
type Point struct {
	Lat, Lon, Ele float64 // degrees, degrees, metres
}

// newPoint validates untrusted input: ok is false for NaN/Inf or out-of-range
// coordinates; implausible elevations become NaN.
func newPoint(lat, lon, ele float64) (Point, bool) {
	if !(lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180) { // also rejects NaN
		return Point{}, false
	}
	if !(ele >= -1000 && ele <= 20000) {
		ele = math.NaN()
	}
	return Point{lat, lon, ele}, true
}
