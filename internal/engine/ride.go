package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
)

const (
	staleAfter   = 3 * time.Second
	closeTimeout = 3 * time.Second
	// maxTickDT caps the time integrated in one tick so a stalled process
	// (laptop suspend, long GC or SIGSTOP) cannot teleport the rider forward.
	maxTickDT = 5 * time.Second
)

type State string

const (
	StateIdle       State = "idle"
	StateConnecting State = "connecting"
	StateReady      State = "ready"
	StateRiding     State = "riding"
	StatePaused     State = "paused"
	StateFinished   State = "finished"
)

type CmdKind string

const (
	CmdStart      CmdKind = "start"
	CmdPause      CmdKind = "pause"
	CmdResume     CmdKind = "resume"
	CmdStop       CmdKind = "stop"
	CmdSetLevel   CmdKind = "set_level"
	CmdLevelDelta CmdKind = "level_delta"
)

type Command struct {
	Kind  CmdKind
	Value int
}

// Tick is one published snapshot of the ride. While paused, Level is the
// floor level the engine sent on pause, not the rider's manual level.
type Tick struct {
	At            time.Time `json:"at"`
	State         State     `json:"state"`
	Mode          string    `json:"mode"`
	ElapsedS      float64   `json:"elapsed_s"`
	DistanceM     float64   `json:"distance_m"`
	SpeedKmh      float64   `json:"speed_kmh"`
	PowerW        float64   `json:"power_w"`
	CadenceRpm    float64   `json:"cadence_rpm"`
	GradePct      float64   `json:"grade_pct"`
	Level         int       `json:"level"`
	ReportedLevel int       `json:"reported_level"`
	Connected     bool      `json:"connected"`
	Alert         string    `json:"alert,omitempty"`
}

type Config struct {
	Driver     bike.Driver
	Mode       Mode // default FreeMode
	StartLevel int
	Physics    Physics       // zero value = DefaultPhysics
	TickEvery  time.Duration // default 1s
}

var (
	ErrBadState       = errors.New("engine: command invalid in this state")
	ErrUnknownCommand = errors.New("engine: unknown command")
	ErrStopped        = errors.New("engine: stopped")
)

type request struct {
	cmd   Command
	reply chan error
}

// Engine owns the ride state; Run is its only mutator.
type Engine struct {
	drv   bike.Driver
	mode  Mode
	phys  Physics
	every time.Duration

	cmds chan request
	done chan struct{}

	// Run-goroutine state.
	state     State
	manual    int
	lastReq   int // last level handed to the driver; 0 = none/force resend
	elapsed   time.Duration
	distance  float64
	speed     float64
	grade     float64
	lastTel   bike.Telemetry
	telAt     time.Time
	haveTel   bool
	connected bool
	alert     string
	lastTick  time.Time

	mu     sync.Mutex // guards snap, subs, closed
	snap   Tick
	subs   map[chan Tick]struct{}
	closed bool
}

func New(c Config) *Engine {
	if c.Mode == nil {
		c.Mode = FreeMode{}
	}
	if c.Physics == (Physics{}) {
		c.Physics = DefaultPhysics()
	}
	if c.TickEvery <= 0 {
		c.TickEvery = time.Second
	}
	e := &Engine{
		drv: c.Driver, mode: c.Mode, phys: c.Physics, every: c.TickEvery,
		cmds:   make(chan request),
		done:   make(chan struct{}),
		state:  StateIdle,
		manual: c.StartLevel,
		subs:   map[chan Tick]struct{}{},
	}
	e.snap = e.build(time.Now())
	return e
}

// Snapshot returns the latest published state.
func (e *Engine) Snapshot() Tick {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snap
}

// Subscribe returns a channel that first carries the current snapshot, then
// every tick. A subscriber whose buffer is full misses ticks. The cancel func
// unsubscribes and closes the channel; the channel is also closed when Run
// returns.
func (e *Engine) Subscribe(buf int) (<-chan Tick, func()) {
	ch := make(chan Tick, max(buf, 1))
	e.mu.Lock()
	ch <- e.snap
	if e.closed {
		close(ch)
		e.mu.Unlock()
		return ch, func() {}
	}
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
	}
}

// Do sends a command to Run and returns its result. After Run returns it
// reports ErrStopped.
func (e *Engine) Do(ctx context.Context, c Command) error {
	r := request{cmd: c, reply: make(chan error, 1)}
	select {
	case e.cmds <- r:
	case <-e.done:
		return ErrStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-r.reply:
		return err
	case <-e.done:
		select {
		case err := <-r.reply:
			return err
		default:
			return ErrStopped
		}
	}
}

// Run drives the ride until a stop command (nil) or ctx cancellation
// (ctx.Err()). It closes the driver and all subscriber channels on exit.
func (e *Engine) Run(ctx context.Context) (err error) {
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		_ = e.drv.Close(cctx)
		cancel()
		e.mu.Lock()
		e.closed = true
		for ch := range e.subs {
			close(ch)
		}
		clear(e.subs)
		e.mu.Unlock()
		close(e.done)
	}()

	e.state = StateConnecting
	e.refresh(time.Now())
	if err := e.drv.Connect(ctx); err != nil {
		return err
	}
	tel, ev := e.drv.Telemetry(), e.drv.Events()
	t := time.NewTicker(e.every)
	defer t.Stop()
	e.lastTick = time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			e.tick(ctx, now)
		case v, ok := <-tel:
			if !ok {
				tel = nil
				continue
			}
			e.lastTel, e.telAt, e.haveTel = v, time.Now(), true
		case v, ok := <-ev:
			if !ok {
				ev = nil
				continue
			}
			e.event(ctx, v)
		case r := <-e.cmds:
			err := e.command(ctx, r.cmd)
			r.reply <- err
			if err == nil && e.state == StateFinished {
				return nil
			}
		}
	}
}

func (e *Engine) caps() (lo, hi int) {
	c := e.drv.Caps()
	lo = max(c.MinLevel, 1)
	return lo, max(c.MaxLevel, lo)
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

// send hands level to the driver when it differs from the last request.
func (e *Engine) send(ctx context.Context, level int) {
	lo, hi := e.caps()
	level = clampInt(level, lo, hi)
	if level == e.lastReq {
		return
	}
	if _, err := e.drv.SetLevel(ctx, level); err == nil {
		e.lastReq = level
	}
}

func (e *Engine) floor(ctx context.Context) {
	lo, _ := e.caps()
	e.send(ctx, lo)
}

// input builds the mode input; stale telemetry counts as no power or cadence.
func (e *Engine) input(now time.Time) ModeInput {
	in := ModeInput{
		Elapsed: e.elapsed, DistanceM: e.distance, SpeedMS: e.speed, Manual: e.manual,
	}
	if e.haveTel && now.Sub(e.telAt) <= staleAfter {
		in.PowerW, in.Cadence = e.lastTel.PowerW, e.lastTel.Cadence
	}
	return in
}

// apply requests the mode's level right now (used by commands).
func (e *Engine) apply(ctx context.Context) {
	e.send(ctx, e.mode.Step(e.input(time.Now())).Level)
}

func (e *Engine) tick(ctx context.Context, now time.Time) {
	dt := min(now.Sub(e.lastTick), maxTickDT)
	e.lastTick = now
	if e.state == StateRiding {
		in := e.input(now)
		e.elapsed += dt
		in.Elapsed = e.elapsed
		out := e.mode.Step(in)
		e.grade = out.Grade
		e.speed = e.phys.Step(e.speed, in.PowerW, out.Grade, dt)
		e.distance += e.speed * dt.Seconds()
		e.send(ctx, out.Level)
	}
	e.refresh(now)
	e.publish()
	e.alert = ""
	e.refresh(now)
}

func (e *Engine) event(ctx context.Context, v bike.Event) {
	switch v.Kind {
	case bike.EventConnected:
		e.connected = true
		if e.state == StateConnecting {
			e.state = StateReady
		}
		if e.state == StateRiding {
			e.lastReq = 0 // re-assert the mode level on the new link
		}
	case bike.EventDisconnected:
		e.connected = false
		if e.state == StateReady {
			e.state = StateConnecting
		}
	case bike.EventLevelUnconfirmed:
		e.alert = fmt.Sprintf("level %d not confirmed (bike reports %d)", v.Want, v.Got)
	case bike.EventWatchdog:
		if e.state == StateRiding {
			e.pause(ctx)
			e.alert = "watchdog: no cadence for 30s"
		}
	}
	e.refresh(time.Now())
}

func (e *Engine) pause(ctx context.Context) {
	e.state = StatePaused
	e.speed, e.grade = 0, 0
	e.floor(ctx)
}

func (e *Engine) command(ctx context.Context, c Command) error {
	if e.state == StateFinished {
		return ErrStopped
	}
	switch c.Kind {
	case CmdStart:
		if e.state != StateReady {
			return ErrBadState
		}
		lo, hi := e.caps()
		e.manual = clampInt(e.manual, lo, hi)
		e.state = StateRiding
		e.lastReq = 0
		e.lastTick = time.Now()
		e.apply(ctx)
	case CmdPause:
		if e.state != StateRiding {
			return ErrBadState
		}
		e.pause(ctx)
	case CmdResume:
		if e.state != StatePaused {
			return ErrBadState
		}
		e.state = StateRiding
		e.lastReq = 0
		e.lastTick = time.Now()
		e.apply(ctx)
	case CmdStop:
		e.floor(ctx)
		e.state = StateFinished
		e.speed, e.grade = 0, 0
		now := time.Now()
		e.refresh(now)
		e.publish()
		return nil
	case CmdSetLevel, CmdLevelDelta:
		lo, hi := e.caps()
		if c.Kind == CmdSetLevel {
			e.manual = clampInt(c.Value, lo, hi)
		} else {
			e.manual = clampInt(e.manual+c.Value, lo, hi)
		}
		if e.state == StateRiding {
			e.apply(ctx)
		}
	default:
		return ErrUnknownCommand
	}
	e.refresh(time.Now())
	return nil
}

// build renders the current state as a Tick.
func (e *Engine) build(now time.Time) Tick {
	tk := Tick{
		At: now, State: e.state, Mode: e.mode.Name(),
		ElapsedS: e.elapsed.Seconds(), DistanceM: e.distance,
		SpeedKmh: e.speed * 3.6, GradePct: e.grade * 100,
		Level: e.manual, ReportedLevel: e.lastTel.Level,
		Connected: e.connected, Alert: e.alert,
	}
	if e.lastReq > 0 {
		tk.Level = e.lastReq
	}
	in := e.input(now)
	tk.PowerW, tk.CadenceRpm = in.PowerW, in.Cadence
	return tk
}

func (e *Engine) refresh(now time.Time) {
	tk := e.build(now)
	e.mu.Lock()
	e.snap = tk
	e.mu.Unlock()
}

// publish fans the current snapshot out without ever blocking.
func (e *Engine) publish() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- e.snap:
		default:
		}
	}
}
