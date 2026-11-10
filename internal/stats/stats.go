// Package stats holds the pure training-metric maths: power curve, zones,
// TSS and friends. It has no dependencies on storage.
package stats

import "math"

// Point is the best average power over DurS seconds.
type Point struct {
	DurS  int
	Watts float64
}

// Durations are the power-curve windows in seconds, 1 s to 2 h.
var Durations = []int{
	1, 2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 25, 30, 40, 45, 60, 75, 90, 120, 150, 180, 240, 300,
	360, 420, 480, 600, 720, 900, 1080, 1200, 1500, 1800, 2100, 2400, 2700, 3000, 3600, 4200,
	4800, 5400, 6000, 6600, 7200,
}

// Prefix fills dst (len n+1, dst[0]=0) with the running sums of vals and returns it.
func Prefix(dst, vals []float64) []float64 {
	dst = dst[:len(vals)+1]
	dst[0] = 0
	for i, v := range vals {
		dst[i+1] = dst[i] + v
	}
	return dst
}

// PowerCurve returns the best mean power for every duration that fits in the
// ride, given the prefix sums of its 1 Hz power (len n+1). O(n·k), no
// per-window allocation.
func PowerCurve(prefix []float64) []Point {
	n := len(prefix) - 1
	out := make([]Point, 0, len(Durations))
	for _, d := range Durations {
		if d > n {
			break
		}
		var best float64
		for i := 0; i+d <= n; i++ {
			if s := prefix[i+d] - prefix[i]; s > best {
				best = s
			}
		}
		out = append(out, Point{DurS: d, Watts: best / float64(d)})
	}
	return out
}

// NP is normalized power from prefix sums: the fourth-root of the mean of the
// fourth powers of the rolling 30 s means. Fewer than 30 samples yields the
// plain mean.
func NP(prefix []float64) float64 {
	const win = 30
	n := len(prefix) - 1
	if n <= 0 {
		return 0
	}
	if n < win {
		return prefix[n] / float64(n)
	}
	var acc float64
	for i := 0; i+win <= n; i++ {
		m := (prefix[i+win] - prefix[i]) / win
		m *= m
		acc += m * m
	}
	return math.Sqrt(math.Sqrt(acc / float64(n-win+1)))
}

// PowerBounds are the upper edges of Coggan zones 1 to 6 (zone 7 is above).
func PowerBounds(ftp float64) [6]float64 {
	return [6]float64{ftp * .55, ftp * .75, ftp * .90, ftp * 1.05, ftp * 1.20, ftp * 1.50}
}

// HRBounds are the upper edges of heart-rate zones 1 to 4 (zone 5 is above).
func HRBounds(maxHR float64) [4]float64 {
	return [4]float64{maxHR * .60, maxHR * .70, maxHR * .80, maxHR * .90}
}

// ZoneOf returns the 0-based zone of v: the first bound v does not exceed
// the strict upper edge of, or len(bounds) above all.
func ZoneOf(v float64, bounds []float64) int {
	for i, b := range bounds {
		if v < b {
			return i
		}
	}
	return len(bounds)
}

// IF is the intensity factor NP/FTP; false when FTP is not positive.
func IF(np, ftp float64) (float64, bool) {
	if ftp <= 0 {
		return 0, false
	}
	return np / ftp, true
}

// TSS is the training stress score for durS seconds at normalized power np.
func TSS(durS, np, ftp float64) (float64, bool) {
	f, ok := IF(np, ftp)
	if !ok {
		return 0, false
	}
	return durS * np * f / (ftp * 3600) * 100, true
}

// VI is the variability index NP/average power; false when avg is not positive.
func VI(np, avg float64) (float64, bool) {
	if avg <= 0 {
		return 0, false
	}
	return np / avg, true
}

// EF is the efficiency factor NP/average heart rate; false without heart rate.
func EF(np, avgHR float64) (float64, bool) {
	if avgHR <= 0 {
		return 0, false
	}
	return np / avgHR, true
}

// MinDecouplingS is the shortest ride (seconds) with a meaningful decoupling.
const MinDecouplingS = 1200

// Decoupling is the percentage the power:HR ratio falls from the first to the
// second half of an n-second ride. False for rides under MinDecouplingS or
// when a half lacks power or heart rate.
func Decoupling(n int, p1, h1, p2, h2 float64) (float64, bool) {
	if n < MinDecouplingS || h1 <= 0 || h2 <= 0 || p1 <= 0 || p2 <= 0 {
		return 0, false
	}
	e1, e2 := p1/h1, p2/h2
	return (e1 - e2) / e1 * 100, true
}

// VAM is the average climbing rate in metres per hour; false without climbing.
func VAM(ascentM, durS float64) (float64, bool) {
	if ascentM <= 0 || durS <= 0 {
		return 0, false
	}
	return ascentM / durS * 3600, true
}

// CompletedFraction is the share of a route that counts as finished.
const CompletedFraction = 0.98

// Completed reports whether distM covers the route; false for an unknown route.
func Completed(distM, routeM float64) bool {
	return routeM > 0 && distM >= CompletedFraction*routeM
}
