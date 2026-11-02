package sensors

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metril/wattson/internal/hass"
)

// HAConfig configures a Home Assistant heart rate source. Zero durations select the defaults.
type HAConfig struct {
	// Dial opens an authenticated client; it is called once per attempt and the
	// monitor closes what it returns.
	Dial       func(ctx context.Context) (*hass.Client, error)
	Entity     string        // entity id whose numeric state is the heart rate in BPM
	BackoffMin time.Duration // default 1s
	BackoffMax time.Duration // default 30s
}

// HAMonitor reads heart rate from a Home Assistant entity, reconnecting with
// backoff forever. Its failures are reported through Status only.
type HAMonitor struct {
	c HAConfig

	last atomic.Pointer[hrSample]

	mu        sync.Mutex
	connected bool
	errText   string
}

var _ Monitor = (*HAMonitor)(nil)

// NewHA returns a monitor for c; call Run to start it.
func NewHA(c HAConfig) *HAMonitor {
	if c.BackoffMin <= 0 {
		c.BackoffMin = time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	return &HAMonitor{c: c}
}

func (m *HAMonitor) Latest() (int, time.Time) {
	if s := m.last.Load(); s != nil {
		return s.bpm, s.at
	}
	return 0, time.Time{}
}

func (m *HAMonitor) Status() HRStatus {
	bpm, at := m.Latest()
	m.mu.Lock()
	defer m.mu.Unlock()
	return HRStatus{Source: "ha", Target: m.c.Entity, Connected: m.connected, BPM: bpm, LastAt: at, Error: m.errText}
}

func (m *HAMonitor) set(f func()) {
	m.mu.Lock()
	f()
	m.mu.Unlock()
}

// Run connects and reconnects until ctx is done.
func (m *HAMonitor) Run(ctx context.Context) error {
	defer m.set(func() { m.connected = false })
	return backoffLoop(ctx, func(ctx context.Context) (bool, error) {
		got, err := m.attempt(ctx)
		if ctx.Err() == nil {
			m.set(func() {
				m.connected = false
				if err != nil {
					m.errText = err.Error()
				}
			})
		}
		return got, err
	}, m.c.BackoffMin, m.c.BackoffMax)
}

// maxBPM bounds a plausible reading; it matches the 16-bit Heart Rate Measurement range.
const maxBPM = 65535

// parseBPM reads a Home Assistant state as beats per minute. Non-numeric
// states (unavailable, unknown) and values that round to 0 or less are no reading.
func parseBPM(state string) (int, bool) {
	f, err := strconv.ParseFloat(state, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	b := math.Round(f)
	if b <= 0 || b > maxBPM {
		return 0, false
	}
	return int(b), true
}

// attempt makes one connection and holds it until it drops. got reports whether
// at least one reading arrived.
func (m *HAMonitor) attempt(ctx context.Context) (got bool, err error) {
	cl, err := m.c.Dial(ctx)
	if err != nil {
		return false, err
	}
	defer cl.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var gotAny, seen atomic.Bool
	err = cl.SubscribeEntities(sctx, []string{m.c.Entity}, func(s hass.State) {
		if s.EntityID != m.c.Entity {
			return
		}
		seen.Store(true)
		if b, ok := parseBPM(s.State); ok {
			m.last.Store(&hrSample{bpm: b, at: time.Now()})
			gotAny.Store(true)
		}
	})
	if err != nil {
		return false, err
	}
	// SubscribeEntities returns after the initial state event, so an entity
	// that is still unseen does not exist.
	if !seen.Load() {
		return false, fmt.Errorf("entity %s not found", m.c.Entity)
	}
	m.set(func() { m.connected, m.errText = true, "" })
	select {
	case <-ctx.Done():
		return gotAny.Load(), nil
	case <-cl.Done():
		return gotAny.Load(), cl.Err()
	}
}
