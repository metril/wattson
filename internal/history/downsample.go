package history

import (
	"math"
	"slices"

	"github.com/metril/wattson/internal/store"
)

// Downsample thins ss to at most max points by averaging buckets of
// ceil(n/max) samples (ElapsedS and DistanceM are taken from each bucket's
// last sample). Input within max is returned as a copy.
func Downsample(ss []store.Sample, max int) []store.Sample {
	if max < 1 || len(ss) <= max {
		return slices.Clone(ss)
	}
	size := (len(ss) + max - 1) / max
	out := make([]store.Sample, 0, (len(ss)+size-1)/size)
	for i := 0; i < len(ss); i += size {
		out = append(out, bucket(ss[i:min(i+size, len(ss))]))
	}
	return out
}

func bucket(b []store.Sample) store.Sample {
	o := store.Sample{Seq: b[0].Seq, At: b[0].At}
	var lv float64
	var hr float64
	var hrN int
	var lat, lon, ele nanMean
	for _, s := range b {
		o.ElapsedS += s.ElapsedS
		o.DistanceM += s.DistanceM
		o.SpeedKmh += s.SpeedKmh
		o.PowerW += s.PowerW
		o.CadenceRpm += s.CadenceRpm
		o.GradePct += s.GradePct
		lv += float64(s.Level)
		if s.HRBpm > 0 {
			hr += float64(s.HRBpm)
			hrN++
		}
		lat.add(s.Lat)
		lon.add(s.Lon)
		ele.add(s.EleM)
	}
	n := float64(len(b))
	o.ElapsedS /= n
	o.DistanceM /= n
	o.SpeedKmh /= n
	o.PowerW /= n
	o.CadenceRpm /= n
	o.GradePct /= n
	o.Level = int(math.Round(lv / n))
	if hrN > 0 {
		o.HRBpm = int(math.Round(hr / float64(hrN)))
	}
	o.Lat, o.Lon, o.EleM = lat.mean(), lon.mean(), ele.mean()
	// Distance and time run to the bucket's end so the chart ends at the true end.
	last := b[len(b)-1]
	o.ElapsedS, o.DistanceM = last.ElapsedS, last.DistanceM
	return o
}

type nanMean struct {
	sum float64
	n   int
}

func (m *nanMean) add(v float64) {
	if !math.IsNaN(v) {
		m.sum += v
		m.n++
	}
}

func (m nanMean) mean() float64 {
	if m.n == 0 {
		return math.NaN()
	}
	return m.sum / float64(m.n)
}
