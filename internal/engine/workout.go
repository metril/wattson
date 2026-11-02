package engine

import (
	"errors"
	"time"
)

// WorkoutBlock is one resolved step of a workout timeline. Interval workouts
// use Level; ERG workouts use TargetW (the rider's watts).
type WorkoutBlock struct {
	Duration time.Duration
	Level    int
	TargetW  float64
}

// blockAt locates elapsed on the timeline using half-open [start,end) blocks.
// Past the end it reports the last block with nothing left and done set.
func blockAt(bs []WorkoutBlock, elapsed time.Duration) (i int, left time.Duration, done bool) {
	if len(bs) == 0 {
		return 0, 0, true
	}
	if elapsed < 0 {
		elapsed = 0
	}
	var start time.Duration
	for i, b := range bs {
		if end := start + b.Duration; elapsed < end {
			return i, end - elapsed, false
		} else {
			start = end
		}
	}
	return len(bs) - 1, 0, true
}

// WorkoutInfo is implemented by modes that ride a workout; blocks is 0 when
// the mode is not a workout.
type WorkoutInfo interface{ WorkoutInfo() (id int64, blocks int) }

// Targeted is implemented by ERG modes, which hold a target power.
type Targeted interface {
	SetTarget(w float64) error
	NudgeTarget(steps int)
}

const (
	MinTargetW = 50   // lowest accepted ERG target, watts
	MaxTargetW = 1000 // highest accepted ERG target, watts
	ErgNudgeW  = 10   // watts per level_delta step in ERG
)

// ErrBadValue means a command value is outside its allowed range.
var ErrBadValue = errors.New("engine: value out of range")
