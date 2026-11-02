package engine

import "time"

// ModeInput is what a Mode sees each tick.
type ModeInput struct {
	Elapsed         time.Duration
	DistanceM       float64
	SpeedMS, PowerW float64
	Cadence         float64
	Manual          int // the rider's manual level
}

// ModeOutput is the resistance level to request and the simulated grade
// (a fraction) fed to the physics.
type ModeOutput struct {
	Level int
	Grade float64
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
