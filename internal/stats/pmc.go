package stats

import "math"

// Time constants of the performance-management chart, in days.
const (
	CTLDays = 42 // fitness
	ATLDays = 7  // fatigue
)

// PMCPoint is one day of the performance-management chart. TSB (form) is CTL-ATL.
type PMCPoint struct {
	Day           int // day ordinal, see DayOrd
	TSS           float64
	CTL, ATL, TSB float64
}

// PMC runs the exponentially weighted CTL (42 d) and ATL (7 d) over every day
// from..to inclusive; days missing from tss count as zero load. Both start at 0.
func PMC(tss map[int]float64, from, to int) []PMCPoint {
	if to < from {
		return nil
	}
	kc, ka := 1-math.Exp(-1.0/CTLDays), 1-math.Exp(-1.0/ATLDays)
	out := make([]PMCPoint, 0, to-from+1)
	var ctl, atl float64
	for d := from; d <= to; d++ {
		l := tss[d]
		ctl += (l - ctl) * kc
		atl += (l - atl) * ka
		out = append(out, PMCPoint{Day: d, TSS: l, CTL: ctl, ATL: atl, TSB: ctl - atl})
	}
	return out
}

// EFTP is the estimated FTP: 95 % of the best 20-minute power. ok is false
// when there is no 20-minute point.
func EFTP(curve []Point) (float64, bool) {
	for _, p := range curve {
		if p.DurS == 1200 && p.Watts > 0 {
			return 0.95 * p.Watts, true
		}
	}
	return 0, false
}
