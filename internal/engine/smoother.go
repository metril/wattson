package engine

import (
	"math"
	"time"
)

// Smoother turns a jittery target into stable level changes: a dead band,
// a minimum dwell between changes and a per-change step limit.
type Smoother struct {
	Hysteresis float64       // change only when |target − cur| ≥ 0.5 + Hysteresis
	MinDwell   time.Duration // minimum time between changes
	MaxStep    int           // max levels per change (<= 0 = unlimited)
	Min, Max   int           // output clamp; Min is floored at 1

	cur    int
	last   time.Duration
	primed bool
}

// DefaultSmoother returns the standard smoother for a rider range.
func DefaultSmoother(min, max int) Smoother {
	return Smoother{Hysteresis: 0.5, MinDwell: 2 * time.Second, MaxStep: 2, Min: min, Max: max}
}

func (s *Smoother) bounds() (lo, hi int) {
	lo = max(1, s.Min)
	return lo, max(lo, s.Max)
}

// Step returns the level for target at time now (monotonic since ride start).
// The first call primes with clamp(round(target)) and counts as a change.
func (s *Smoother) Step(target float64, now time.Duration) int {
	lo, hi := s.bounds()
	if math.IsNaN(target) {
		if !s.primed {
			return lo
		}
		return s.cur
	}
	if !s.primed {
		s.primed = true
		s.cur = clampInt(roundInt(target), lo, hi)
		s.last = now
		return s.cur
	}
	if math.Abs(target-float64(s.cur)) < 0.5+s.Hysteresis || now-s.last < s.MinDwell {
		return s.cur
	}
	delta := roundInt(target) - s.cur
	if s.MaxStep > 0 {
		delta = clampInt(delta, -s.MaxStep, s.MaxStep)
	}
	s.cur = clampInt(s.cur+delta, lo, hi)
	s.last = now
	return s.cur
}

// roundInt rounds half away from zero, saturating for huge or infinite values.
func roundInt(v float64) int {
	return int(math.Max(-1e9, math.Min(1e9, math.Round(v))))
}
