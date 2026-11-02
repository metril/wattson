// Package habike drives a bike through Home Assistant: levels are written with
// number.set_value to a resistance entity (for example an ESPHome FTMS bridge)
// and telemetry is merged from the resistance, power and cadence entities.
// It implements bike.Session, so every level still passes through the
// bike.Supervisor.
//
// Only FTMS-style bridges that expose a number entity are supported; there is
// no FitPlus path through Home Assistant.
package habike

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/hass"
)

// ErrEntityUnavailable means the resistance entity is missing, unavailable or
// unknown; the returned error's message is "home assistant entity <id> is unavailable".
var ErrEntityUnavailable error = entityErr{}

type entityErr struct{ id string }

func (e entityErr) Error() string {
	if e.id == "" {
		return "home assistant entity is unavailable"
	}
	return "home assistant entity " + e.id + " is unavailable"
}

func (e entityErr) Is(target error) bool { _, ok := target.(entityErr); return ok }

const (
	defaultPeriod = time.Second
	defaultMax    = 16
	telemetryBuf  = 16
	setBound      = 5 * time.Second
	floorBound    = 2 * time.Second
)

// Config configures the driver. Zero values select defaults.
type Config struct {
	// Dial opens an authenticated Home Assistant client; the session owns and closes it.
	Dial func(ctx context.Context) (*hass.Client, error)
	// Resistance is the number.* entity set by SetLevel; Power and Cadence are
	// sensor.* entities (either may be empty).
	Resistance, Power, Cadence string
	// MinLevel and MaxLevel are the rider range. MaxLevel 0 means the entity's max.
	MinLevel, MaxLevel int
	// Period is the telemetry period; default 1s.
	Period time.Duration
}

func unavailable(id string) error { return entityErr{id: id} }

// Dial returns a bike.Dial: each call dials Home Assistant, subscribes to the
// entities and returns a fresh Session.
func Dial(c Config) bike.Dial {
	return func(ctx context.Context) (bike.Session, error) {
		if c.Dial == nil {
			return nil, errors.New("habike: no Home Assistant dialer")
		}
		cl, err := c.Dial(ctx)
		if err != nil {
			return nil, err
		}
		s := &Session{
			cl: cl, cfg: c, states: map[string]hass.State{}, bad: make(chan struct{}, 1),
			tel: make(chan bike.Telemetry, telemetryBuf), done: make(chan struct{}), exited: make(chan struct{}),
		}
		if s.cfg.Period <= 0 {
			s.cfg.Period = defaultPeriod
		}
		ids := []string{c.Resistance}
		for _, id := range []string{c.Power, c.Cadence} {
			if id != "" {
				ids = append(ids, id)
			}
		}
		// The subscription outlives ctx; ctx only bounds its setup.
		sctx, cancel := context.WithCancel(context.Background())
		stop := context.AfterFunc(ctx, cancel)
		err = cl.SubscribeEntities(sctx, ids, s.store)
		stop()
		if err == nil {
			s.mu.Lock()
			st, ok := s.states[c.Resistance]
			s.mu.Unlock()
			if !ok || !numeric(st.State) {
				err = unavailable(c.Resistance)
			} else if rerr := s.rangeErr(); rerr != nil {
				err = rerr
			}
		}
		if err != nil {
			cancel()
			_ = cl.Close()
			return nil, err
		}
		s.cancel = cancel
		go s.run()
		return s, nil
	}
}

// Session is one live Home Assistant link; it implements bike.Session.
type Session struct {
	cl     *hass.Client
	cfg    Config
	cancel context.CancelFunc

	tel    chan bike.Telemetry
	done   chan struct{}
	exited chan struct{}

	bad chan struct{} // the resistance entity went non-numeric; run ends the session

	mu      sync.Mutex // guards states
	states  map[string]hass.State
	smu     sync.Mutex // serialises writes; guards last and closed
	last    int        // last level sent; 0 = none
	closed  bool
	endOnce sync.Once
	emu     sync.Mutex
	endErr  error
}

var _ bike.Session = (*Session)(nil)

// store runs on the client's read goroutine: it must only cache.
func (s *Session) store(st hass.State) {
	s.mu.Lock()
	s.states[st.EntityID] = st
	s.mu.Unlock()
	if st.EntityID == s.cfg.Resistance && !numeric(st.State) {
		select {
		case s.bad <- struct{}{}:
		default:
		}
	}
}

func numeric(v string) bool {
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func (s *Session) value(id string) float64 {
	if id == "" {
		return 0
	}
	s.mu.Lock()
	st := s.states[id]
	s.mu.Unlock()
	f, err := strconv.ParseFloat(st.State, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func (s *Session) attr(name string) (float64, bool) {
	s.mu.Lock()
	st := s.states[s.cfg.Resistance]
	s.mu.Unlock()
	f, ok := st.Attributes[name].(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// bounds is the allowed level range: never below 1, rider range intersected
// with the entity's own min and max.
func (s *Session) bounds() (lo, hi int) {
	lo = max(1, s.cfg.MinLevel)
	if m, ok := s.attr("min"); ok {
		lo = max(lo, int(math.Ceil(m-1e-9)))
	}
	hi = s.cfg.MaxLevel
	if m, ok := s.attr("max"); ok && m >= 1 {
		em := int(math.Floor(m + 1e-9))
		if hi <= 0 {
			hi = em
		} else {
			hi = min(hi, em)
		}
	}
	if hi <= 0 {
		hi = defaultMax
	}
	return lo, max(hi, lo)
}

// rangeErr refuses an entity whose own minimum is above the rider's maximum
// level (or whose maximum is below the lowest allowed level): no level would
// be both safe for the rider and accepted by the entity.
func (s *Session) rangeErr() error {
	lo := max(1, s.cfg.MinLevel)
	if m, ok := s.attr("min"); ok {
		lo = max(lo, int(math.Ceil(m-1e-9)))
	}
	hi := s.cfg.MaxLevel
	if m, ok := s.attr("max"); ok && m >= 1 {
		if em := int(math.Floor(m + 1e-9)); hi <= 0 || em < hi {
			hi = em
		}
	}
	if hi > 0 && lo > hi {
		return fmt.Errorf("habike: entity %s allows levels from %d but the allowed maximum is %d", s.cfg.Resistance, lo, hi)
	}
	return nil
}

// snap rounds level to the nearest multiple of the entity's step (counted from
// its min, or 0 when it has none) and keeps it within [lo, hi]. Steps below 1
// are ignored: levels are whole numbers.
func (s *Session) snap(level, lo, hi int) int {
	step, ok := s.attr("step")
	if !ok || step < 1 {
		return level
	}
	origin, _ := s.attr("min")
	v := origin + math.Round((float64(level)-origin)/step)*step
	for v > float64(hi)+1e-9 {
		v -= step
	}
	for v < float64(lo)-1e-9 {
		v += step
	}
	return bike.ClampLevel(int(math.Round(v)), lo, hi)
}

func (s *Session) Protocol() bike.Protocol          { return bike.ProtoHA }
func (s *Session) MaxLevel() int                    { _, hi := s.bounds(); return hi }
func (s *Session) Telemetry() <-chan bike.Telemetry { return s.tel }
func (s *Session) Done() <-chan struct{}            { return s.done }

// Err wraps blue.ErrDisconnected once the Home Assistant link is lost; nil
// while the session runs or after a normal Close.
func (s *Session) Err() error {
	s.emu.Lock()
	defer s.emu.Unlock()
	return s.endErr
}

func (s *Session) end(err error) {
	s.endOnce.Do(func() {
		s.emu.Lock()
		s.endErr = err
		s.emu.Unlock()
		close(s.done)
	})
}

func (s *Session) SetLevel(ctx context.Context, level int) (int, error) {
	return s.set(ctx, level, false)
}

func (s *Session) Resend(ctx context.Context, level int) (int, error) {
	return s.set(ctx, level, true)
}

func (s *Session) set(ctx context.Context, level int, force bool) (int, error) {
	s.smu.Lock()
	defer s.smu.Unlock()
	if s.closed {
		return 0, bike.ErrClosed
	}
	select {
	case <-s.done:
		return 0, s.Err()
	default:
	}
	lo, hi := s.bounds()
	level = s.snap(bike.ClampLevel(level, lo, hi), lo, hi)
	if !force && level == s.last {
		return level, nil
	}
	if err := s.write(ctx, level, setBound); err != nil {
		return 0, err
	}
	return level, nil
}

// write calls number.set_value; smu must be held. last is invalidated first:
// after a failure the entity may or may not have changed.
func (s *Session) write(ctx context.Context, level int, bound time.Duration) error {
	s.last = 0
	wctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	err := s.cl.CallService(wctx, "number", "set_value", s.cfg.Resistance, map[string]any{"value": level})
	if err != nil {
		return err
	}
	s.last = level
	return nil
}

// Close sets the floor level (best effort, 2s while the link is up), closes the
// client and Telemetry. It is idempotent.
func (s *Session) Close(ctx context.Context) error {
	s.smu.Lock()
	if s.closed {
		s.smu.Unlock()
		<-s.exited
		return nil
	}
	s.closed = true
	var werr error
	select {
	case <-s.done:
	default:
		lo, _ := s.bounds()
		werr = s.write(ctx, lo, floorBound)
	}
	s.end(nil)
	s.smu.Unlock()
	s.cancel()
	_ = s.cl.Close()
	<-s.exited
	return werr
}

func (s *Session) run() {
	defer close(s.exited)
	defer close(s.tel)
	t := time.NewTicker(s.cfg.Period)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.bad:
			// The bridge lost the bike: end the session so the supervisor
			// redials and re-asserts the target once the entity is back.
			s.end(fmt.Errorf("habike: %w: %w", blue.ErrDisconnected, unavailable(s.cfg.Resistance)))
			return
		case <-s.cl.Done():
			err := s.cl.Err()
			if err == nil {
				err = hass.ErrClosed
			}
			s.end(fmt.Errorf("habike: %w: %w", blue.ErrDisconnected, err))
			return
		case <-t.C:
			v := bike.Telemetry{
				Level:   int(math.Round(s.value(s.cfg.Resistance))),
				Cadence: s.value(s.cfg.Cadence),
				PowerW:  s.value(s.cfg.Power),
				At:      time.Now(),
			}
			for { // single producer: drop the oldest when full
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
