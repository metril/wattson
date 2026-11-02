package engine

import "time"

// ModeInput is what a Mode sees each tick.
type ModeInput struct {
	Elapsed         time.Duration
	DistanceM       float64
	SpeedMS, PowerW float64
	Cadence         float64
	Manual          int // the rider's manual level
	Bias            int // live level bias (route mode), within ±MaxBias
}

// ModeOutput is the resistance level to request and the simulated grade
// (a fraction) fed to the physics.
type ModeOutput struct {
	Level      int
	Grade      float64 // under the rider, fraction
	GradeAhead float64 // at the lookahead point, fraction (route mode)
	Done       bool    // the ride is complete and should finish

	TargetW   float64       // ERG target power in watts; 0 when the mode has none
	Block     int           // current workout block index
	BlockLeft time.Duration // time left in the current workout block
}

// Mode decides resistance each tick.
type Mode interface {
	Name() string
	Step(in ModeInput) ModeOutput
}

// FreeMode follows the rider's manual level on flat ground.
type FreeMode struct{}

func (FreeMode) Name() string { return "free" }

func (FreeMode) Step(in ModeInput) ModeOutput { return ModeOutput{Level: in.Manual} }
