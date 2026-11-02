// Package engine runs a ride: it integrates bike physics from telemetry,
// drives resistance through a Mode and fans state out to subscribers.
package engine

import (
	"math"
	"time"
)

const (
	gravity     = 9.80665
	maxGrade    = 0.25
	physicsStep = 100 * time.Millisecond
)

// Physics is a point-mass cycling model.
type Physics struct{ MassKg, CdA, Crr, Rho float64 }

// DefaultPhysics is a 75 kg rider+bike on a road bike position.
func DefaultPhysics() Physics { return Physics{75, 0.36, 0.004, 1.225} }

// Step advances speed v (m/s) by dt under powerW on grade (a fraction),
// integrating with explicit-Euler substeps of at most 100 ms. The result is
// never negative or NaN.
func (p Physics) Step(v, powerW, grade float64, dt time.Duration) float64 {
	if math.IsNaN(v) || v < 0 {
		v = 0
	}
	if dt <= 0 {
		return v
	}
	grade = max(-maxGrade, min(maxGrade, grade))
	theta := math.Atan(grade)
	sin, cos := math.Sincos(theta)
	powerW = max(powerW, 0)
	n := int((dt + physicsStep - 1) / physicsStep)
	h := dt.Seconds() / float64(n)
	for range n {
		f := powerW/max(v, 1) -
			p.MassKg*gravity*sin -
			p.Crr*p.MassKg*gravity*cos -
			0.5*p.Rho*p.CdA*v*math.Abs(v)
		v += f / p.MassKg * h
		if math.IsNaN(v) || v < 0 {
			v = 0
		}
	}
	return v
}
