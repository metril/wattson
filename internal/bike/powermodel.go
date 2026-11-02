package bike

import (
	"math"
	"sync"
)

// PowerParams are the coefficients of P = rpm·(A + B·level), in watts.
type PowerParams struct{ A, B float64 }

// Seed equals the simulator's power model for level ≥ 1.
var Seed = PowerParams{A: 0.375, B: 0.25}

const (
	pmForget     = 0.999 // RLS forgetting factor λ
	pmMinB       = 0.01  // smallest slope a fit may have and still be valid
	pmMinRPM     = 40
	pmMaxRPM     = 160
	pmMaxWatts   = 2000
	pmOutlierN   = 20 // outlier rejection starts after this many samples
	pmOutlierFrc = 0.5
)

// PowerModelState is the persisted fit. P holds the symmetric 2×2 covariance
// as p11, p12, p22; N counts accepted samples.
type PowerModelState struct {
	A float64    `json:"a" doc:"Intercept coefficient: watts per rpm at level 0."`
	B float64    `json:"b" doc:"Slope coefficient: watts per rpm per resistance level."`
	P [3]float64 `json:"p" doc:"Covariance of the fit as p11, p12, p22."`
	N int        `json:"n" doc:"Number of accepted samples."`
}

// SeedState is the state of an untrained model.
func SeedState() PowerModelState {
	return PowerModelState{A: Seed.A, B: Seed.B, P: [3]float64{1, 0, 1}}
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func (s PowerModelState) valid() bool {
	return finite(s.A, s.B, s.P[0], s.P[1], s.P[2]) && s.B >= pmMinB && s.P[0] > 0 && s.P[2] > 0 && s.N >= 0
}

// PowerModel is a 2-parameter recursive-least-squares fit of
// P = rpm·(A + B·level). It is safe for concurrent use.
type PowerModel struct {
	mu sync.Mutex
	s  PowerModelState
}

// NewPowerModel restores a model; an invalid state yields the seed state.
func NewPowerModel(s PowerModelState) *PowerModel {
	if !s.valid() {
		s = SeedState()
	}
	return &PowerModel{s: s}
}

// Observe feeds one settled (level, cadence, power) sample and reports whether
// it was accepted. Garbage and outliers are rejected without changing state.
func (m *PowerModel) Observe(level int, rpm, watts float64) bool {
	if !finite(rpm, watts) || level < 1 || rpm < pmMinRPM || rpm > pmMaxRPM || watts <= 0 || watts > pmMaxWatts {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.s
	x1, x2 := rpm, rpm*float64(level)
	pred := s.A*x1 + s.B*x2
	if s.N >= pmOutlierN && pred > 0 && math.Abs(watts-pred) > pmOutlierFrc*pred {
		return false
	}
	p11, p12, p22 := s.P[0], s.P[1], s.P[2]
	// Px
	a, b := p11*x1+p12*x2, p12*x1+p22*x2
	den := pmForget + x1*a + x2*b
	if den <= 0 || !finite(den) {
		return false
	}
	k1, k2 := a/den, b/den
	err := watts - pred
	n := PowerModelState{A: s.A + k1*err, B: s.B + k2*err, N: s.N + 1}
	// P = (P − k·(xᵀP)) / λ, xᵀP = (Px)ᵀ by symmetry.
	n.P[0] = (p11 - k1*a) / pmForget
	n.P[1] = (p12 - (k1*b+k2*a)/2) / pmForget
	n.P[2] = (p22 - k2*b) / pmForget
	if !finite(n.A, n.B, n.P[0], n.P[1], n.P[2]) {
		return false
	}
	m.s = n
	return true
}

// Params are the fitted coefficients, or Seed when the fit is not valid.
func (m *PowerModel) Params() PowerParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paramsLocked()
}

func (m *PowerModel) paramsLocked() PowerParams {
	if !finite(m.s.A, m.s.B) || m.s.B < pmMinB {
		return Seed
	}
	return PowerParams{A: m.s.A, B: m.s.B}
}

// Power is the modelled watts at level and rpm.
func (m *PowerModel) Power(level int, rpm float64) float64 {
	p := m.Params()
	return rpm * (p.A + p.B*float64(level))
}

// LevelFor is the continuous level that yields watts at rpm; NaN when rpm ≤ 0.
func (m *PowerModel) LevelFor(watts, rpm float64) float64 {
	if rpm <= 0 || !finite(rpm, watts) {
		return math.NaN()
	}
	p := m.Params()
	return (watts/rpm - p.A) / p.B
}

// Slope is dP/dlevel in watts per level at rpm.
func (m *PowerModel) Slope(rpm float64) float64 {
	return rpm * m.Params().B
}

// State returns the persistable fit.
func (m *PowerModel) State() PowerModelState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}
