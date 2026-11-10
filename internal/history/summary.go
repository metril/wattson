// Package history derives ride summaries from 1 Hz samples and thins them
// for charting.
package history

import (
	"math"

	"github.com/metril/wattson/internal/stats"
	"github.com/metril/wattson/internal/store"
)

// Params are the rider and route values a ride's metrics are computed with.
// Zero FTPW, MaxHRBpm or RouteDistanceM mean unknown.
type Params struct {
	FTPW, MaxHRBpm           int
	WeightKg, RouteDistanceM float64
}

// ParamsOf returns the snapshot stored on the ride.
func ParamsOf(r store.Ride) Params {
	return Params{FTPW: r.FTPW, MaxHRBpm: r.HRMaxBpm, WeightKg: r.WeightKg, RouteDistanceM: r.RouteDistanceM}
}

// Summarize computes the aggregate figures and training metrics of a ride in
// one pass over ss, sharing one prefix-sum array between normalized power and
// the power curve. Empty input yields a Summary holding only the stats version.
func Summarize(ss []store.Sample, p Params) store.Summary {
	if len(ss) == 0 {
		return store.Summary{StatsVersion: store.StatsVersion}
	}
	n := len(ss)
	first, last := ss[0], ss[n-1]
	sum := store.Summary{
		DurationS:    last.ElapsedS,
		ElapsedS:     last.At.Sub(first.At).Seconds(),
		DistanceM:    last.DistanceM,
		AscentM:      Ascent(ss),
		StatsVersion: store.StatsVersion,
	}
	prefix := make([]float64, n+1)
	var pz, hz []float64
	var pb [6]float64
	var hb [4]float64
	if p.FTPW > 0 {
		pb = stats.PowerBounds(float64(p.FTPW))
		pz = make([]float64, len(pb)+1)
	}
	if p.MaxHRBpm > 0 {
		hb = stats.HRBounds(float64(p.MaxHRBpm))
		hz = make([]float64, len(hb)+1)
	}
	var cad, hr, p1, p2, h1, h2 float64
	var cadN, hrN, hr1N, hr2N int
	half := n / 2
	for i, s := range ss {
		prefix[i+1] = prefix[i] + s.PowerW
		sum.MaxPowerW = math.Max(sum.MaxPowerW, s.PowerW)
		sum.MaxSpeedKmh = math.Max(sum.MaxSpeedKmh, s.SpeedKmh)
		sum.MaxCadenceRpm = math.Max(sum.MaxCadenceRpm, s.CadenceRpm)
		if s.CadenceRpm > 0 {
			cad += s.CadenceRpm
			cadN++
		}
		if pz != nil {
			pz[stats.ZoneOf(s.PowerW, pb[:])]++
		}
		if i < half {
			p1 += s.PowerW
		} else {
			p2 += s.PowerW
		}
		if s.HRBpm > 0 {
			h := float64(s.HRBpm)
			hr += h
			hrN++
			sum.MaxHRBpm = max(sum.MaxHRBpm, s.HRBpm)
			if hz != nil {
				hz[stats.ZoneOf(h, hb[:])]++
			}
			if i < half {
				h1 += h
				hr1N++
			} else {
				h2 += h
				hr2N++
			}
		}
	}
	pow := prefix[n]
	sum.AvgPowerW = pow / float64(n)
	sum.KJ = pow / 1000 // one sample per second
	sum.NPW = stats.NP(prefix)
	sum.Curve = stats.PowerCurve(prefix)
	if cadN > 0 {
		sum.AvgCadenceRpm = cad / float64(cadN)
	}
	if hrN > 0 {
		sum.AvgHRBpm = int(math.Round(hr / float64(hrN)))
		sum.HRZones = hz
		if v, ok := stats.EF(sum.NPW, hr/float64(hrN)); ok {
			sum.EF = &v
		}
		if hr1N > 0 && hr2N > 0 {
			if v, ok := stats.Decoupling(n, p1/float64(half), h1/float64(hr1N), p2/float64(n-half), h2/float64(hr2N)); ok {
				sum.DecouplingPct = &v
			}
		}
	}
	if sum.DurationS > 0 {
		sum.AvgSpeedKmh = sum.DistanceM / sum.DurationS * 3.6
	}
	if p.FTPW > 0 {
		sum.PowerZones = pz
		ftp := float64(p.FTPW)
		if v, ok := stats.IF(sum.NPW, ftp); ok {
			sum.IntensityFactor = &v
		}
		if v, ok := stats.TSS(sum.DurationS, sum.NPW, ftp); ok {
			sum.TSS = &v
		}
	}
	if v, ok := stats.VI(sum.NPW, sum.AvgPowerW); ok {
		sum.VI = &v
	}
	if v, ok := stats.VAM(sum.AscentM, sum.DurationS); ok {
		sum.VAM = &v
	}
	sum.Completed = stats.Completed(sum.DistanceM, p.RouteDistanceM)
	return sum
}

// NormalizedPower is the fourth-root of the mean of the fourth powers of the
// rolling 30-sample means. Fewer than 30 samples yields the plain mean.
func NormalizedPower(watts []float64) float64 {
	return stats.NP(stats.Prefix(make([]float64, len(watts)+1), watts))
}

// Ascent totals climbing with a 1 m dead band. It uses recorded elevation
// when any sample has it; otherwise elevation is integrated from
// grade_pct x distance travelled between samples.
func Ascent(ss []store.Sample) float64 {
	ele := make([]float64, 0, len(ss))
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
