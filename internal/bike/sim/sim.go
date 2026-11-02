// Package sim is a deterministic simulated bike session for development
// without hardware and for tests.
package sim

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
)

const (
	defaultMax    = 16
	defaultCadRPM = 80
	telemetryBuf  = 16
)

// Config configures a simulated session. Zero values select defaults.
type Config struct {
	MaxLevel int                                 // default 16
	MinLevel int                                 // level Close applies; default 1 (clamped to >= 1)
	Cadence  func(elapsed time.Duration) float64 // rpm; default Const(80)
	Period   time.Duration                       // telemetry interval; default 1s
}

// Power is the simulated power model: rpm·(0.625 + 0.25·(max(level,1)−1)),
// 0 when rpm ≤ 0.
func Power(level int, rpm float64) float64 {
	if rpm <= 0 {
		return 0
	}
	return rpm * (0.625 + 0.25*float64(max(level, 1)-1))
}

// Const returns a cadence function that always reports rpm.
func Const(rpm float64) func(time.Duration) float64 {
	return func(time.Duration) float64 { return rpm }
}

// Session is a simulated bike link; it implements bike.Session.
type Session struct {
	max     int
	min     int
	cadence func(time.Duration) float64
	period  time.Duration
	start   time.Time

	tel    chan bike.Telemetry
	done   chan struct{}
	exited chan struct{}

	mu     sync.Mutex
	cur    int // level reported in telemetry
	last   int // last level written; 0 = none yet
	levels []int
	err    error
	closed bool
}

var _ bike.Session = (*Session)(nil)

// New starts a simulated session and its telemetry goroutine, which exits when
// the session ends (Close or Drop).
func New(c Config) *Session {
	if c.MaxLevel < 1 {
		c.MaxLevel = defaultMax
	}
	c.MinLevel = min(max(c.MinLevel, 1), c.MaxLevel)
	if c.Cadence == nil {
		c.Cadence = Const(defaultCadRPM)
	}
	if c.Period <= 0 {
		c.Period = time.Second
	}
	s := &Session{
		max:     c.MaxLevel,
		min:     c.MinLevel,
		cadence: c.Cadence,
		period:  c.Period,
		start:   time.Now(),
		tel:     make(chan bike.Telemetry, telemetryBuf),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
		cur:     c.MinLevel,
	}
	go s.run()
	return s
}

// Dial returns a bike.Dial that creates a fresh Session per call.
func Dial(c Config) bike.Dial {
	return func(context.Context) (bike.Session, error) { return New(c), nil }
}

func (s *Session) Protocol() bike.Protocol          { return bike.ProtoSim }
func (s *Session) MaxLevel() int                    { return s.max }
func (s *Session) Telemetry() <-chan bike.Telemetry { return s.tel }
func (s *Session) Done() <-chan struct{}            { return s.done }

// Err is nil until the link drops.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Levels returns every level applied, in order (including Close's floor).
func (s *Session) Levels() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.levels)
}

func (s *Session) SetLevel(_ context.Context, level int) (int, error) { return s.set(level, false) }
func (s *Session) Resend(_ context.Context, level int) (int, error)   { return s.set(level, true) }

func (s *Session) set(level int, force bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.downLocked(); err != nil {
		return 0, err
	}
	level = bike.ClampLevel(level, 1, s.max)
	if force || level != s.last {
		s.applyLocked(level)
	}
	return level, nil
}

func (s *Session) applyLocked(level int) {
	s.cur, s.last = level, level
	s.levels = append(s.levels, level)
}

// downLocked reports why writes are refused, or nil while the link is up.
func (s *Session) downLocked() error {
	if s.closed {
		return bike.ErrClosed
	}
	return s.err
}

// Drop simulates link loss: Done closes and Err wraps blue.ErrDisconnected.
func (s *Session) Drop() {
	s.mu.Lock()
	if !s.isDoneLocked() {
		s.err = fmt.Errorf("sim: %w", blue.ErrDisconnected)
		close(s.done)
	}
	s.mu.Unlock()
}

func (s *Session) isDoneLocked() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Close applies the floor level (when the link is up), ends the session and
// closes Telemetry. It is idempotent.
func (s *Session) Close(context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if !s.isDoneLocked() {
			s.applyLocked(s.min)
			close(s.done)
		}
	}
	s.mu.Unlock()
	<-s.exited
	return nil
}

func (s *Session) run() {
	defer close(s.exited)
	defer close(s.tel)
	t := time.NewTicker(s.period)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			cur := s.cur
			s.mu.Unlock()
			c := s.cadence(time.Since(s.start))
			v := bike.Telemetry{Level: cur, Cadence: c, PowerW: Power(cur, c), At: time.Now()}
			// Single producer: drop the oldest when the buffer is full.
			for {
				select {
				case s.tel <- v:
				default:
					select {
					case <-s.tel:
					default:
					}
					continue
				}
				break
			}
		}
	}
}
