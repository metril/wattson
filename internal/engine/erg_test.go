package engine

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
)

func seedModel() *bike.PowerModel { return bike.NewPowerModel(bike.SeedState()) }

func TestErgFeedforward(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		w     float64
		level int
	}{{150, 6}, {250, 11}} {
		m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: c.w, MinLevel: 1, MaxLevel: 16})
		out := m.Step(ModeInput{Cadence: 80})
		if out.Level != c.level || out.TargetW != c.w {
			t.Errorf("target %v: %+v, want level %d", c.w, out, c.level)
		}
	}
}

func TestErgMultiplier(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 250, Multiplier: 1.25, MinLevel: 1, MaxLevel: 16})
	if out := m.Step(ModeInput{Cadence: 80}); out.Level != 9 || out.TargetW != 250 {
		t.Fatalf("%+v, want level 9 target 250", out)
	}
}

func TestErgNeverBelowFloor(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 50, MinLevel: 0, MaxLevel: 16})
	for _, cad := range []float64{0, 25, 100, 160} {
		for _, p := range []float64{0, 40, 1000} {
			if out := m.Step(ModeInput{Cadence: cad, PowerW: p, Elapsed: time.Duration(p) * time.Second}); out.Level < 1 {
				t.Fatalf("cadence %v power %v: level %d", cad, p, out.Level)
			}
		}
	}
}

// recMode records every level the ERG mode asks for.
type recMode struct {
	*ErgMode
	mu     sync.Mutex
	levels []int
}

func (r *recMode) Step(in ModeInput) ModeOutput {
	out := r.ErgMode.Step(in)
	r.mu.Lock()
	r.levels = append(r.levels, out.Level)
	r.mu.Unlock()
	return out
}

func (r *recMode) asked() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.levels)
}

type ergRig struct {
	e    *Engine
	sub  <-chan Tick
	rec  *recMode
	res  chan error
	stop func()
	mu   sync.Mutex
	sess []*sim.Session
}

// writes are the levels the simulated bike received, in order.
func (r *ergRig) writes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int
	for _, s := range r.sess {
		out = append(out, s.Levels()...)
	}
	return out
}

// newErgRig starts a free or workout ERG ride on a simulated bike whose
// cadence follows cad (virtual time since the bubble started; the ride starts
// 1 s in, so ride time = cad time − 1 s).
func newErgRig(t *testing.T, cad func(time.Duration) float64, c ErgConfig) *ergRig {
	t.Helper()
	r := &ergRig{}
	dial := func(ctx context.Context) (bike.Session, error) {
		s, _ := sim.Dial(sim.Config{Cadence: cad})(ctx)
		r.mu.Lock()
		r.sess = append(r.sess, s.(*sim.Session))
		r.mu.Unlock()
		return s, nil
	}
	if c.Model == nil {
		c.Model = seedModel()
	}
	c.MinLevel, c.MaxLevel = 1, 16
	r.rec = &recMode{ErgMode: NewErgMode(c)}
	r.e = New(Config{Driver: bike.NewSupervisor(dial, bike.SupervisorOptions{MinLevel: 1, MaxLevel: 16}), TickEvery: time.Second})
	sub, cancelSub := r.e.Subscribe(1024)
	r.sub = sub
	var stop func()
	r.res, stop = start(t, r.e)
	r.stop = func() { stop(); cancelSub() }
	time.Sleep(time.Second)
	synctest.Wait()
	if err := r.e.Do(context.Background(), Command{Kind: "start", Mode: r.rec}); err != nil {
		t.Fatal(err)
	}
	return r
}

// within reports the first riding tick in [from, to] seconds whose power is
// further than tol·T from T, or false when all are inside.
func offTarget(ticks []Tick, from, to, target, tol float64) (Tick, bool) {
	for _, tk := range ticks {
		if tk.ElapsedS >= from && tk.ElapsedS <= to && math.Abs(tk.PowerW-target) > tol*target+1e-9 {
			return tk, true
		}
	}
	return Tick{}, false
}

func levelChanges(ticks []Tick, from, to float64) int {
	n, prev := 0, 0
	for _, tk := range ticks {
		if tk.ElapsedS < from || tk.ElapsedS > to {
			continue
		}
		if prev != 0 && tk.Level != prev {
			n++
		}
		prev = tk.Level
	}
	return n
}

func TestErgConverges(t *testing.T) {
	t.Parallel()
	for _, target := range []float64{150, 200, 250} {
		synctest.Test(t, func(t *testing.T) {
			r := newErgRig(t, sim.Const(80), ErgConfig{TargetW: target})
			defer r.stop()
			ticks := advance(120*time.Second, r.sub)
			if tk, bad := offTarget(ticks, 20, 120, target, 0.05); bad {
				t.Fatalf("T=%v: off target at %v s: power %v level %d", target, tk.ElapsedS, tk.PowerW, tk.Level)
			}
			if n := levelChanges(ticks, 20, 120); n > 1 {
				t.Fatalf("T=%v: %d level changes in the window", target, n)
			}
			tk := lastTick(t, ticks)
			if tk.TargetPowerW != target || tk.Mode != "erg" {
				t.Fatalf("tick %+v", tk)
			}
		})
	}
}

func TestErgConvergesMisSeeded(t *testing.T) {
	t.Parallel()
	for _, target := range []float64{150, 200, 250} {
		synctest.Test(t, func(t *testing.T) {
			bad := bike.NewPowerModel(bike.PowerModelState{A: 0.3, B: 0.3, P: [3]float64{1, 0, 1}})
			r := newErgRig(t, sim.Const(80), ErgConfig{TargetW: target, Model: bad})
			defer r.stop()
			ticks := advance(120*time.Second, r.sub)
			if tk, off := offTarget(ticks, 45, 120, target, 0.05); off {
				t.Fatalf("T=%v: off target at %v s: power %v level %d", target, tk.ElapsedS, tk.PowerW, tk.Level)
			}
		})
	}
}

func TestErgCadenceAware(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cad := func(d time.Duration) float64 {
			if d >= 61*time.Second { // ride time 60 s
				return 96
			}
			return 80
		}
		r := newErgRig(t, cad, ErgConfig{TargetW: 200})
		defer r.stop()
		ticks := advance(120*time.Second, r.sub)
		if tk, off := offTarget(ticks, 70, 120, 200, 0.05); off {
			t.Fatalf("off target at %v s: power %v cadence %v level %d", tk.ElapsedS, tk.PowerW, tk.CadenceRpm, tk.Level)
		}
	})
}

func TestErgAntiWindup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newErgRig(t, sim.Const(80), ErgConfig{TargetW: 600})
		defer r.stop()
		for _, tk := range advance(60*time.Second, r.sub) {
			if tk.ElapsedS >= 3 && tk.Level != 16 {
				t.Fatalf("at %v s level %d, want 16", tk.ElapsedS, tk.Level)
			}
		}
		if err := do(t, r.e, CmdSetTargetPower, 150); err != nil {
			t.Fatal(err)
		}
		ticks := advance(30*time.Second, r.sub)
		if tk, off := offTarget(ticks, 64, 90, 150, 0.05); off { // within 4 ticks, and stays
			t.Fatalf("off target at %v s: power %v level %d", tk.ElapsedS, tk.PowerW, tk.Level)
		}
		if len(ticks) < 30 {
			t.Fatalf("only %d ticks", len(ticks))
		}
	})
}

func TestErgBelowFloorNeverZero(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newErgRig(t, sim.Const(100), ErgConfig{TargetW: 50})
		defer r.stop()
		for _, tk := range advance(30*time.Second, r.sub) {
			if tk.ElapsedS >= 3 && tk.Level != 1 {
				t.Fatalf("at %v s level %d, want 1", tk.ElapsedS, tk.Level)
			}
		}
		if err := do(t, r.e, CmdSetTargetPower, 200); err != nil {
			t.Fatal(err)
		}
		// 200 W at 100 rpm sits between L6 (187.5 W) and L7 (212.5 W): the
		// best a whole level can do is half a level step, 12.5 W.
		ticks := advance(8*time.Second, r.sub)
		for _, tk := range ticks[min(5, len(ticks)-1):] {
			if math.Abs(tk.PowerW-200) > 12.5+1e-9 {
				t.Fatalf("at %v s power %v, want within 12.5 W of 200", tk.ElapsedS, tk.PowerW)
			}
		}
		for _, l := range append(r.writes(), r.rec.asked()...) {
			if l < 1 {
				t.Fatalf("level %d requested", l)
			}
		}
	})
}

func TestErgLowCadenceHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cad := func(d time.Duration) float64 {
			if d >= 61*time.Second && d < 81*time.Second { // ride time 60..80 s
				return 0
			}
			return 80
		}
		r := newErgRig(t, cad, ErgConfig{TargetW: 200})
		defer r.stop()
		advance(60*time.Second, r.sub)
		before := len(r.writes())
		hold := advance(20*time.Second, r.sub)
		if got := len(r.writes()); got != before {
			t.Fatalf("%d driver writes while not pedalling", got-before)
		}
		if tk := lastTick(t, hold); tk.State != StateRiding {
			t.Fatalf("state %s", tk.State)
		}
		ticks := advance(8*time.Second, r.sub)
		if tk, off := offTarget(ticks, 86, 100, 200, 0.05); off {
			t.Fatalf("off target at %v s: power %v level %d", tk.ElapsedS, tk.PowerW, tk.Level)
		}
		for _, tk := range append(hold, ticks...) {
			if tk.Level > 10 {
				t.Fatalf("level %d at %v s exceeded 10", tk.Level, tk.ElapsedS)
			}
		}
		for _, l := range r.rec.asked() {
			if l > 10 {
				t.Fatalf("mode asked for level %d", l)
			}
		}
	})
}

func TestErgWorkout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		blocks := []WorkoutBlock{{Duration: 30 * time.Second, TargetW: 150}, {Duration: 30 * time.Second, TargetW: 250}}
		r := newErgRig(t, sim.Const(80), ErgConfig{WorkoutID: 7, Blocks: blocks})
		ticks := advance(60*time.Second, r.sub) // Run returns at 60 s, so no r.stop
		if tk, off := offTarget(ticks, 25, 25, 150, 0.05); off {
			t.Fatalf("not at 150 W by 25 s: power %v", tk.PowerW)
		}
		if tk, off := offTarget(ticks, 45, 59, 250, 0.05); off {
			t.Fatalf("block 1: off target at %v s: power %v", tk.ElapsedS, tk.PowerW)
		}
		for _, tk := range ticks {
			if tk.ElapsedS >= 45 && tk.ElapsedS <= 59 && (tk.BlockIndex != 1 || tk.TargetPowerW != 250) {
				t.Fatalf("at %v s: %+v", tk.ElapsedS, tk)
			}
		}
		last := lastTick(t, ticks)
		if last.State != StateFinished || last.WorkoutID != 7 || last.BlockCount != 2 {
			t.Fatalf("last tick %+v, want finished", last)
		}
		if err := <-r.res; err != nil {
			t.Fatalf("Run: %v", err)
		}
		if w := r.writes(); len(w) == 0 || w[len(w)-1] != 1 {
			t.Fatalf("writes %v, want last 1", w)
		}
	})
}

func TestErgNudge(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		startWith(t, e, f, NewErgMode(ErgConfig{Model: seedModel(), TargetW: 200, MinLevel: 1, MaxLevel: 16}))
		if err := do(t, e, "level_delta", 2); err != nil {
			t.Fatal(err)
		}
		if tk := lastTick(t, advance(time.Second, sub)); tk.TargetPowerW != 220 {
			t.Fatalf("free ERG target %v, want 220", tk.TargetPowerW)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		blocks := []WorkoutBlock{{Duration: time.Minute, TargetW: 200}}
		startWith(t, e, f, NewErgMode(ErgConfig{Blocks: blocks, Model: seedModel(), MinLevel: 1, MaxLevel: 16}))
		if err := do(t, e, CmdSetTargetPower, 300); !errors.Is(err, ErrBadState) {
			t.Fatalf("set_target_power in a workout: %v, want ErrBadState", err)
		}
		if err := do(t, e, "level_delta", -20); err != nil {
			t.Fatal(err)
		}
		if tk := lastTick(t, advance(time.Second, sub)); tk.TargetPowerW != 100 {
			t.Fatalf("workout target %v, want 200-100", tk.TargetPowerW)
		}
	})
}

func BenchmarkTickErg(b *testing.B) {
	f := newFake()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 200, MinLevel: 1, MaxLevel: 16})
	e := New(Config{Driver: f, Mode: m})
	e.state, e.connected = StateRiding, true
	e.lastTel, e.haveTel = bike.Telemetry{Level: 8, Cadence: 80, PowerW: 190}, true
	ctx := context.Background()
	now := time.Now()
	e.lastTick = now
	b.ReportAllocs()
	for b.Loop() {
		now = now.Add(time.Second)
		e.telAt = now
		e.tick(ctx, now)
	}
}

func TestErgHoldFreezesIntegrator(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 200, MinLevel: 1, MaxLevel: 16})
	el := time.Duration(0)
	step := func(cad, p float64) ModeOutput {
		el += time.Second
		return m.Step(ModeInput{Elapsed: el, Cadence: cad, PowerW: p})
	}
	var held ModeOutput
	for range 10 {
		held = step(80, 150) // persistent shortfall: the level climbs
	}
	integ := m.integ
	for range 30 {
		if out := step(10, 900); out.Level != held.Level {
			t.Fatalf("level %d while not pedalling, want %d", out.Level, held.Level)
		}
	}
	if m.integ != integ {
		t.Fatalf("integrator moved while not pedalling: %v -> %v", integ, m.integ)
	}
	// Never started pedalling: the first level comes from the nominal cadence.
	n := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 200, MinLevel: 1, MaxLevel: 16})
	if out := n.Step(ModeInput{Cadence: 0}); out.Level != 9 {
		t.Fatalf("initial hold level %d, want 9", out.Level)
	}
}

func TestErgResumeNoSpike(t *testing.T) {
	t.Parallel()
	for _, target := range []float64{200, 250} {
		synctest.Test(t, func(t *testing.T) {
			r := newErgRig(t, sim.Const(80), ErgConfig{TargetW: target})
			defer r.stop()
			pre := lastTick(t, advance(40*time.Second, r.sub)).Level
			if err := do(t, r.e, "pause", 0); err != nil {
				t.Fatal(err)
			}
			advance(5*time.Second, r.sub)
			n := len(r.rec.asked())
			if err := do(t, r.e, "resume", 0); err != nil { // the rider kept pedalling
				t.Fatal(err)
			}
			ticks := advance(30*time.Second, r.sub)
			for _, l := range r.rec.asked()[n:] {
				if l > pre+1 {
					t.Fatalf("T=%v: asked for level %d after resume, pre-pause level %d", target, l, pre)
				}
			}
			for _, tk := range ticks {
				if tk.Level > pre+1 {
					t.Fatalf("T=%v: tick level %d at %v s, pre-pause level %d", target, tk.Level, tk.ElapsedS, pre)
				}
			}
			if tk, off := offTarget(ticks, 50, 80, target, 0.05); off {
				t.Fatalf("T=%v: off target at %v s: power %v", target, tk.ElapsedS, tk.PowerW)
			}
		})
	}
}

func TestErgCadenceRampNoMax(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cad := func(d time.Duration) float64 { // ride time = d - 1 s
			switch s := d.Seconds() - 1; {
			case s < 5:
				return 0
			case s < 10:
				return 35
			case s < 15:
				return 50
			}
			return 80
		}
		r := newErgRig(t, cad, ErgConfig{TargetW: 200})
		defer r.stop()
		ticks := advance(60*time.Second, r.sub)
		for _, tk := range ticks {
			if tk.Level >= 16 {
				t.Fatalf("level %d at %v s (cadence %v)", tk.Level, tk.ElapsedS, tk.CadenceRpm)
			}
		}
		for _, l := range r.rec.asked() {
			if l >= 16 {
				t.Fatalf("mode asked for max level %d", l)
			}
		}
		if tk, off := offTarget(ticks, 40, 60, 200, 0.05); off {
			t.Fatalf("off target at %v s: power %v level %d", tk.ElapsedS, tk.PowerW, tk.Level)
		}
	})
}

func TestErgNilModelFallsBackToSeed(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{TargetW: 150, MinLevel: 1, MaxLevel: 16})
	if out := m.Step(ModeInput{Cadence: 80}); out.Level != 6 {
		t.Fatalf("level %d, want 6", out.Level)
	}
}

func TestErgNoReadingNoFeedback(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 150, MinLevel: 1, MaxLevel: 16})
	if out := m.Step(ModeInput{Cadence: 80, PowerW: 0}); out.Level != 6 {
		t.Fatalf("level %d with no power reading, want 6", out.Level)
	}
}

func TestErgRiseLimited(t *testing.T) {
	t.Parallel()
	m := NewErgMode(ErgConfig{Model: seedModel(), TargetW: 150, MinLevel: 1, MaxLevel: 16})
	if out := m.Step(ModeInput{Cadence: 80}); out.Level != 6 {
		t.Fatalf("level %d, want 6", out.Level)
	}
	_ = m.SetTarget(250)
	el := time.Second
	for _, want := range []int{8, 10, 11} { // ff is 11: +2 per tick
		if out := m.Step(ModeInput{Cadence: 80, Elapsed: el}); out.Level != want {
			t.Fatalf("at %v level %d, want %d", el, out.Level, want)
		}
		el += time.Second
	}
	_ = m.SetTarget(100) // falls are not limited
	if out := m.Step(ModeInput{Cadence: 80, Elapsed: el}); out.Level != 4 {
		t.Fatalf("fall: level %d, want 4", out.Level)
	}
}
