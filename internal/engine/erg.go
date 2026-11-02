package engine

import (
	"math"
	"time"

	"github.com/metril/wattson/internal/bike"
)

const (
	ergKp          = 0.5 // proportional gain on the power error
	ergKi          = 0.1 // integral gain, 1/s
	ergIMaxLevels  = 2   // integrator bound, in levels' worth of watts
	ergHyst        = 0.6 // levels the request must move before the level changes
	ergSettleTicks = 2   // ticks without feedback after a level change
	ergMinRpm      = 30  // below this the rider is not pedalling: hold
	ergNominalRpm  = 80  // cadence assumed for the first level while not pedalling
	ergFFMinRpm    = 60  // feed-forward cadence floor, so a pedalling ramp does not demand max
	ergMaxRise     = 2   // most levels the request may rise in one tick (falls are unlimited)
	ergAdjMaxW     = 100 // bound of the workout target adjustment, watts
)

// ErgConfig configures an ErgMode.
type ErgConfig struct {
	WorkoutID int64
	Blocks    []WorkoutBlock // nil = free ERG
	TargetW   float64        // free ERG starting target, rider watts
	Model     *bike.PowerModel
	// Multiplier is the rider's power multiplier; 0 or invalid = 1.
	Multiplier float64
	MinLevel   int
	MaxLevel   int
}

// ErgMode holds a target power: the learned inverse power model gives the
// feed-forward level, and a small PI trim on the power error (with
// conditional-integration anti-windup) corrects what the model gets wrong.
// Free ERG has no blocks and never finishes; workout ERG follows the block
// timeline.
type ErgMode struct {
	id     int64
	blocks []WorkoutBlock
	model  *bike.PowerModel
	mult   float64
	lo, hi int

	base float64 // free ERG target, rider watts
	adj  float64 // workout ERG target adjustment, rider watts

	integ       float64 // PI integrator, watts (bike)
	settle      int     // ticks left to ignore feedback after a level change or target change
	lastTarget  float64
	stepped     bool // Step has run at least once
	cur         int  // level currently requested; 0 = none yet
	lastElapsed time.Duration
}

// NewErgMode returns an ERG mode; c.Model is required.
func NewErgMode(c ErgConfig) *ErgMode {
	lo := max(c.MinLevel, 1)
	mult := c.Multiplier
	if !(mult > 0) || math.IsInf(mult, 0) {
		mult = 1
	}
	if c.Model == nil {
		c.Model = bike.NewPowerModel(bike.SeedState())
	}
	return &ErgMode{
		id: c.WorkoutID, blocks: c.Blocks, model: c.Model, mult: mult,
		lo: lo, hi: max(c.MaxLevel, lo), base: clampTarget(c.TargetW),
	}
}

func (*ErgMode) Name() string { return "erg" }

// WorkoutInfo returns the workout id and block count; 0, 0 for free ERG.
func (m *ErgMode) WorkoutInfo() (id int64, blocks int) { return m.id, len(m.blocks) }

func clampTarget(w float64) float64 {
	if math.IsNaN(w) {
		return MinTargetW
	}
	return math.Max(MinTargetW, math.Min(MaxTargetW, w))
}

// Target is the current target in rider watts.
func (m *ErgMode) Target() float64 {
	if len(m.blocks) == 0 {
		return m.base
	}
	i, _, _ := blockAt(m.blocks, m.lastElapsed)
	return clampTarget(m.blocks[i].TargetW + m.adj)
}

// SetTarget sets the free ERG target; workouts own their targets.
func (m *ErgMode) SetTarget(w float64) error {
	if len(m.blocks) > 0 {
		return ErrBadState
	}
	m.base = clampTarget(w)
	return nil
}

// NudgeTarget moves the target by ErgNudgeW per step: the base target in free
// ERG, or the adjustment (±ergAdjMaxW) in a workout.
func (m *ErgMode) NudgeTarget(steps int) {
	d := float64(ErgNudgeW * steps)
	if len(m.blocks) == 0 {
		m.base = clampTarget(m.base + d)
		return
	}
	m.adj = math.Max(-ergAdjMaxW, math.Min(ergAdjMaxW, m.adj+d))
}

// level rounds a continuous level request into [lo, hi]; NaN gives lo.
func (m *ErgMode) level(u float64) int {
	if math.IsNaN(u) {
		return m.lo
	}
	return clampInt(int(math.Round(math.Max(float64(m.lo), math.Min(float64(m.hi), u)))), m.lo, m.hi)
}

func (m *ErgMode) Step(in ModeInput) ModeOutput {
	dt := max(in.Elapsed-m.lastElapsed, 0).Seconds()
	m.lastElapsed = in.Elapsed
	target := m.Target()
	out := ModeOutput{TargetW: target}
	if len(m.blocks) > 0 {
		i, left, done := blockAt(m.blocks, in.Elapsed)
		out.Block, out.BlockLeft, out.Done = i, left, done
	}

	if target != m.lastTarget || (m.stepped && dt == 0) {
		// The measured power still reflects the old target's level (or, on
		// a command-driven Step such as resume, the paused floor level), so
		// ignore it until the bike has caught up with the new feed-forward.
		m.lastTarget = target
		m.settle = ergSettleTicks
	}
	m.stepped = true
	bt := target / m.mult // the target in bike watts
	if in.Cadence < ergMinRpm {
		// Not pedalling: hold the level and freeze the integrator, so
		// pedalling again does not spike.
		if m.cur == 0 {
			m.cur = m.level(m.model.LevelFor(bt, ergNominalRpm))
		}
		out.Level = m.cur
		return out
	}
	// Feed-forward and gain use a floored cadence: while the rider ramps up
	// from a stop, the true cadence would demand the maximum level. Accepted
	// by design: steady riding at 30-60 rpm may run about 10 % under target,
	// because the feed-forward is for 60 rpm and the integrator is capped.
	rpm := math.Max(in.Cadence, ergFFMinRpm)
	ff := m.model.LevelFor(bt, rpm)
	s := math.Max(m.model.Slope(rpm), 1e-3)
	var err float64
	if in.PowerW > 0 { // a 0 W reading while pedalling is a dropout, not feedback
		err = bt - in.PowerW/m.mult
	}
	db := math.Min(0.5*s, 0.05*bt)
	e := err
	if m.settle > 0 || math.Abs(err) <= db {
		e = 0
	}
	if dt > 0 && m.settle > 0 {
		m.settle--
	}
	u := ff + (ergKp*e+m.integ)/s
	// Conditional integration: do not wind up while the level is saturated
	// and the error pushes further into the limit.
	if dt > 0 && !((u >= float64(m.hi) && e > 0) || (u <= float64(m.lo) && e < 0)) {
		lim := ergIMaxLevels * s
		m.integ = math.Max(-lim, math.Min(lim, m.integ+ergKi*e*dt))
		u = ff + (ergKp*e+m.integ)/s
	}
	if m.cur == 0 || math.Abs(u-float64(m.cur)) >= ergHyst {
		next := m.level(u)
		if dt > 0 && m.cur != 0 && next > m.cur+ergMaxRise {
			next = m.cur + ergMaxRise
		}
		if next != m.cur {
			m.settle = ergSettleTicks
		}
		m.cur = next
	}
	out.Level = m.cur
	return out
}
