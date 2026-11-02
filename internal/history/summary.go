// Package history derives ride summaries from 1 Hz samples and thins them
// for charting.
package history

import (
	"math"

	"github.com/metril/wattson/internal/store"
)

// Summarize computes the aggregate figures of a ride. Empty input yields the
// zero Summary.
func Summarize(ss []store.Sample) store.Summary {
	if len(ss) == 0 {
		return store.Summary{}
	}
	first, last := ss[0], ss[len(ss)-1]
	sum := store.Summary{
		DurationS: last.ElapsedS,
		ElapsedS:  last.At.Sub(first.At).Seconds(),
		DistanceM: last.DistanceM,
		AscentM:   Ascent(ss),
	}
	watts := make([]float64, len(ss))
	var pow, cad, hr float64
	var cadN, hrN int
	for i, s := range ss {
		watts[i] = s.PowerW
		pow += s.PowerW
		sum.MaxPowerW = math.Max(sum.MaxPowerW, s.PowerW)
		sum.MaxSpeedKmh = math.Max(sum.MaxSpeedKmh, s.SpeedKmh)
		if s.CadenceRpm > 0 {
			cad += s.CadenceRpm
			cadN++
		}
		if s.HRBpm > 0 {
			hr += float64(s.HRBpm)
			hrN++
			sum.MaxHRBpm = max(sum.MaxHRBpm, s.HRBpm)
		}
	}
	sum.AvgPowerW = pow / float64(len(ss))
	sum.KJ = pow / 1000 // one sample per second
	sum.NPW = NormalizedPower(watts)
	if cadN > 0 {
		sum.AvgCadenceRpm = cad / float64(cadN)
	}
	if hrN > 0 {
		sum.AvgHRBpm = int(math.Round(hr / float64(hrN)))
	}
	if sum.DurationS > 0 {
		sum.AvgSpeedKmh = sum.DistanceM / sum.DurationS * 3.6
	}
	return sum
}

// NormalizedPower is the fourth-root of the mean of the fourth powers of the
// rolling 30-sample means. Fewer than 30 samples yields the plain mean.
func NormalizedPower(watts []float64) float64 {
	const win = 30
	if len(watts) == 0 {
		return 0
	}
	if len(watts) < win {
		var s float64
		for _, w := range watts {
			s += w
		}
		return s / float64(len(watts))
	}
	var roll, acc float64
	n := 0
	for i, w := range watts {
		roll += w
		if i >= win {
			roll -= watts[i-win]
		}
		if i >= win-1 {
			m := roll / win
			acc += m * m * m * m
			n++
		}
	}
	return math.Sqrt(math.Sqrt(acc / float64(n)))
}

// Ascent totals climbing with a 1 m dead band. It uses recorded elevation
// when any sample has it; otherwise elevation is integrated from
// grade_pct x distance travelled between samples.
func Ascent(ss []store.Sample) float64 {
	var ele []float64
	for _, s := range ss {
		if !math.IsNaN(s.EleM) {
			ele = append(ele, s.EleM)
		}
	}
	if len(ele) == 0 {
		ele = make([]float64, len(ss))
		for i := 1; i < len(ss); i++ {
			ele[i] = ele[i-1] + ss[i].GradePct/100*(ss[i].DistanceM-ss[i-1].DistanceM)
		}
	}
	if len(ele) == 0 {
		return 0
	}
	anchor, asc := ele[0], 0.0
	for _, e := range ele[1:] {
		switch d := e - anchor; {
		case d >= 1:
			asc += d
			anchor = e
		case d <= -1:
			anchor = e
		}
	}
	return asc
}
