package engine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
)

type fakeDriver struct {
	tel chan bike.Telemetry
	ev  chan bike.Event

	mu       sync.Mutex
	calls    []int
	closed   bool
	closeErr error
}

func newFake() *fakeDriver {
	return &fakeDriver{tel: make(chan bike.Telemetry, 16), ev: make(chan bike.Event, 16)}
}

func (f *fakeDriver) Connect(context.Context) error { return nil }
func (f *fakeDriver) SetLevel(_ context.Context, l int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, l)
	return l, nil
}
func (f *fakeDriver) Telemetry() <-chan bike.Telemetry { return f.tel }
func (f *fakeDriver) Events() <-chan bike.Event        { return f.ev }
func (f *fakeDriver) Caps() bike.Caps {
	return bike.Caps{MinLevel: 1, MaxLevel: 16, Connected: true}
}
func (f *fakeDriver) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.closeErr
}
func (f *fakeDriver) Calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}
func (f *fakeDriver) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
func (f *fakeDriver) connect() { f.ev <- bike.Event{Kind: bike.EventConnected} }

// start runs e in the bubble and returns its result channel plus a cleanup.
func start(t *testing.T, e *Engine) (chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- e.Run(ctx) }()
	synctest.Wait()
	return res, func() { cancel(); <-res }
}

func do(t *testing.T, e *Engine, k CmdKind, v int) error {
	t.Helper()
	return e.Do(context.Background(), Command{Kind: k, Value: v})
}

// advance sleeps d and returns the ticks received meanwhile.
func advance(d time.Duration, ch <-chan Tick) []Tick {
	time.Sleep(d)
	synctest.Wait()
	var out []Tick
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

func lastTick(t *testing.T, ts []Tick) Tick {
	t.Helper()
	if len(ts) == 0 {
		t.Fatal("no ticks")
	}
	return ts[len(ts)-1]
}

func riding(t *testing.T, f *fakeDriver, e *Engine) {
	t.Helper()
	f.connect()
	synctest.Wait()
	if err := do(t, e, "start", 0); err != nil {
		t.Fatal(err)
	}
}

func TestStateMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		res, stop := start(t, e)
		defer stop()
		step := func(want State) {
			t.Helper()
			if got := lastTick(t, advance(time.Second, sub)).State; got != want {
				t.Fatalf("state %s, want %s", got, want)
			}
		}
		step("connecting")
		f.connect()
		step("ready")
		for _, c := range []struct {
			k    CmdKind
			want State
		}{{"start", "riding"}, {"pause", "paused"}, {"resume", "riding"}} {
			if err := do(t, e, c.k, 0); err != nil {
				t.Fatal(err)
			}
			step(c.want)
		}
		if err := do(t, e, "stop", 0); err != nil {
			t.Fatal(err)
		}
		if err := <-res; err != nil {
			t.Fatalf("Run: %v", err)
		}
		res <- nil // let stop() drain
		if got := e.Snapshot().State; got != "finished" {
			t.Fatalf("final state %s", got)
		}
		if err := do(t, e, "start", 0); !errors.Is(err, ErrStopped) {
			t.Fatalf("got %v, want ErrStopped", err)
		}
		if !f.isClosed() {
			t.Fatal("driver not closed")
		}
	})
}

func TestStartBeforeReady(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		_, stop := start(t, e)
		defer stop()
		if err := do(t, e, "start", 0); !errors.Is(err, ErrBadState) {
			t.Fatalf("start: %v", err)
		}
		f.connect()
		synctest.Wait()
		if err := do(t, e, "resume", 0); !errors.Is(err, ErrBadState) {
			t.Fatalf("resume: %v", err)
		}
		if err := do(t, e, "bogus", 0); !errors.Is(err, ErrUnknownCommand) {
			t.Fatalf("bogus: %v", err)
		}
	})
}

func TestFreeModeLevels(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f, StartLevel: 8})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		riding(t, f, e)
		check := func(want ...int) {
			t.Helper()
			advance(time.Second, sub)
			advance(time.Second, sub) // no extra calls on unchanged ticks
			if got := f.Calls(); !slices.Equal(got, want) {
				t.Fatalf("calls %v, want %v", got, want)
			}
		}
		check(8)
		for _, st := range []struct {
			kind  CmdKind
			v     int
			calls []int
		}{
			{"set_level", 10, []int{8, 10}},
			{"level_delta", -3, []int{8, 10, 7}},
			{"set_level", 0, []int{8, 10, 7, 1}},
		} {
			if err := do(t, e, st.kind, st.v); err != nil {
				t.Fatalf("%s %d: %v", st.kind, st.v, err)
			}
			check(st.calls...)
		}
	})
}

func TestLevelDeltaSaturates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f, StartLevel: 15})
		_, stop := start(t, e)
		defer stop()
		riding(t, f, e)
		if err := do(t, e, "level_delta", 5); err != nil {
			t.Fatal(err)
		}
		if got := e.Snapshot().Level; got != 16 {
			t.Fatalf("level %d, want 16", got)
		}
		if err := do(t, e, "level_delta", -1); err != nil {
			t.Fatal(err)
		}
		if got := e.Snapshot().Level; got != 15 {
			t.Fatalf("level %d, want 15", got)
		}
	})
}

// A level command reaches the driver at once, not on the next 1 Hz tick.
func TestSetLevelAppliesImmediately(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f, StartLevel: 8})
		_, stop := start(t, e)
		defer stop()
		riding(t, f, e)
		time.Sleep(2500 * time.Millisecond) // idle, mid-tick-interval
		t0 := time.Now()
		if err := do(t, e, "set_level", 9); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := f.Calls(); !slices.Equal(got, []int{8, 9}) {
			t.Fatalf("calls %v, want [8 9] before any tick", got)
		}
		if err := do(t, e, "level_delta", 2); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := f.Calls(); !slices.Equal(got, []int{8, 9, 11}) {
			t.Fatalf("calls %v", got)
		}
		if d := time.Since(t0); d != 0 {
			t.Fatalf("virtual time advanced %v", d)
		}
	})
}

func TestSetLevelImmediateReachesSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var sessions []*sim.Session
		dial := func(ctx context.Context) (bike.Session, error) {
			s, _ := sim.Dial(sim.Config{Cadence: sim.Const(85)})(ctx)
			mu.Lock()
			sessions = append(sessions, s.(*sim.Session))
			mu.Unlock()
			return s, nil
		}
		e := New(Config{Driver: bike.NewSupervisor(dial, bike.SupervisorOptions{}), StartLevel: 8})
		_, stop := start(t, e)
		defer stop()
		time.Sleep(time.Second)
		synctest.Wait()
		if err := do(t, e, "start", 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2500 * time.Millisecond) // limiter idle > 1 s
		t0 := time.Now()
		if err := do(t, e, "set_level", 9); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		mu.Lock()
		s := sessions[0]
		mu.Unlock()
		lv := s.Levels()
		if len(lv) == 0 || lv[len(lv)-1] != 9 {
			t.Fatalf("levels %v, want trailing 9", lv)
		}
		if d := time.Since(t0); d != 0 {
			t.Fatalf("took %v", d)
		}
	})
}

func TestPauseSendsMinAndFreezes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f, StartLevel: 8})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		riding(t, f, e)
		for range 5 {
			f.tel <- bike.Telemetry{Level: 8, Cadence: 80, PowerW: 200}
			advance(time.Second, sub)
		}
		before := e.Snapshot()
		if before.SpeedKmh <= 0 || before.DistanceM <= 0 {
			t.Fatalf("not moving: %+v", before)
		}
		if err := do(t, e, "pause", 0); err != nil {
			t.Fatal(err)
		}
		if c := f.Calls(); c[len(c)-1] != 1 {
			t.Fatalf("calls %v, want trailing 1", c)
		}
		dist := e.Snapshot().DistanceM
		for range 5 {
			f.tel <- bike.Telemetry{Level: 1, Cadence: 80, PowerW: 200}
			ts := advance(time.Second, sub)
			if len(ts) != 1 {
				t.Fatalf("got %d ticks per second, want 1", len(ts))
			}
			for _, tk := range ts {
				if tk.SpeedKmh != 0 || tk.DistanceM != dist {
					t.Fatalf("not frozen: %+v (dist %v)", tk, dist)
				}
			}
		}
		if err := do(t, e, "resume", 0); err != nil {
			t.Fatal(err)
		}
		if c := f.Calls(); c[len(c)-1] != 8 {
			t.Fatalf("calls %v, want trailing 8", c)
		}
	})
}

func TestStaleTelemetryIsZeroPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		f.tel <- bike.Telemetry{Level: 8, Cadence: 80, PowerW: 200}
		ts := advance(time.Second, sub)
		if got := lastTick(t, ts).PowerW; got != 200 {
			t.Fatalf("fresh power %v", got)
		}
		ts = advance(3*time.Second, sub) // now +4 s
		if tk := lastTick(t, ts); tk.PowerW != 0 || tk.CadenceRpm != 0 {
			t.Fatalf("stale tick %+v", tk)
		}
	})
}

func TestWatchdogAutoPauses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		riding(t, f, e)
		advance(time.Second, sub)
		f.ev <- bike.Event{Kind: bike.EventWatchdog}
		synctest.Wait()
		ts := advance(time.Second, sub)
		if tk := lastTick(t, ts); tk.State != "paused" || tk.Alert != "watchdog: no cadence for 30s" {
			t.Fatalf("tick %+v", tk)
		}
		if tk := lastTick(t, advance(time.Second, sub)); tk.Alert != "" || tk.State != "paused" {
			t.Fatalf("next tick %+v", tk)
		}
	})
}

func TestLevelUnconfirmedAlert(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, cancelSub := e.Subscribe(64)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		f.ev <- bike.Event{Kind: bike.EventLevelUnconfirmed, Want: 9, Got: 7}
		synctest.Wait()
		if tk := lastTick(t, advance(time.Second, sub)); tk.Alert != "level 9 not confirmed (bike reports 7)" {
			t.Fatalf("tick %+v", tk)
		}
	})
}

func TestFanOutNonBlocking(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		a, cancelA := e.Subscribe(1)
		defer cancelA()
		b, cancelB := e.Subscribe(64)
		defer cancelB()
		_, stop := start(t, e)
		defer stop()
		<-b // initial snapshot
		got := advance(10*time.Second, b)
		if len(got) != 10 {
			t.Fatalf("B got %d ticks", len(got))
		}
		for i := 1; i < len(got); i++ {
			if d := got[i].At.Sub(got[i-1].At); d != time.Second {
				t.Fatalf("gap %v", d)
			}
		}
		if len(a) != 1 {
			t.Fatalf("A holds %d, want 1", len(a))
		}
		if last := <-a; !last.At.Equal(got[len(got)-1].At) {
			t.Fatalf("A holds %v, want newest %v", last.At, got[len(got)-1].At)
		}
	})
}

func TestSubscribeSnapshotFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f, StartLevel: 6})
		_, stop := start(t, e)
		defer stop()
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		sub, cancelSub := e.Subscribe(4)
		defer cancelSub()
		if first, snap := <-sub, e.Snapshot(); first != snap {
			t.Fatalf("first %+v != snapshot %+v", first, snap)
		}
	})
}

func TestCancelClosesDriver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, _ := e.Subscribe(4)
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() { res <- e.Run(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-res; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
		if !f.isClosed() {
			t.Fatal("driver not closed")
		}
		for range sub { // drains then must be closed
		}
	})
}

func TestRideConvergesOnSim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := bike.NewSupervisor(sim.Dial(sim.Config{Cadence: sim.Const(85)}), bike.SupervisorOptions{})
		e := New(Config{Driver: d, StartLevel: 8})
		sub, cancelSub := e.Subscribe(512)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		time.Sleep(time.Second)
		synctest.Wait()
		if err := do(t, e, "start", 0); err != nil {
			t.Fatal(err)
		}
		tk := lastTick(t, advance(300*time.Second, sub))
		if tk.State != "riding" || tk.PowerW != 201.875 || tk.SpeedKmh < 32 || tk.SpeedKmh > 34 {
			t.Fatalf("tick %+v", tk)
		}
	})
}

func TestRideSurvivesReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var sessions []*sim.Session
		dial := func(ctx context.Context) (bike.Session, error) {
			s, _ := sim.Dial(sim.Config{Cadence: sim.Const(85)})(ctx)
			mu.Lock()
			sessions = append(sessions, s.(*sim.Session))
			mu.Unlock()
			return s, nil
		}
		e := New(Config{Driver: bike.NewSupervisor(dial, bike.SupervisorOptions{}), StartLevel: 8})
		sub, cancelSub := e.Subscribe(512)
		defer cancelSub()
		_, stop := start(t, e)
		defer stop()
		time.Sleep(time.Second)
		synctest.Wait()
		if err := do(t, e, "start", 0); err != nil {
			t.Fatal(err)
		}
		advance(59*time.Second, sub)
		mu.Lock()
		first := sessions[0]
		mu.Unlock()
		first.Drop()
		ts := advance(10*time.Second, sub)
		mu.Lock()
		if len(sessions) < 2 {
			mu.Unlock()
			t.Fatal("no second session")
		}
		second := sessions[1]
		mu.Unlock()
		if lv := second.Levels(); len(lv) == 0 || lv[0] != 8 {
			t.Fatalf("session 2 levels %v", lv)
		}
		last := lastTick(t, ts)
		if last.State != "riding" || last.DistanceM <= ts[0].DistanceM {
			t.Fatalf("ticks first %+v last %+v", ts[0], last)
		}
	})
}

func BenchmarkTick(b *testing.B) {
	f := newFake()
	e := New(Config{Driver: f, StartLevel: 8})
	e.state = "riding"
	e.lastReq = 8
	e.speed = 9
	ctx := context.Background()
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		now = now.Add(time.Second)
		e.tick(ctx, now)
	}
}

func TestCloseErrSurfacesDriverClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		f.closeErr = bike.ErrNoSession
		e := New(Config{Driver: f})
		_, stop := start(t, e)
		stop()
		if err := e.CloseErr(); !errors.Is(err, bike.ErrNoSession) {
			t.Fatalf("CloseErr = %v", err)
		}
	})
}

func TestFinalTickOnCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		e := New(Config{Driver: f})
		sub, _ := e.Subscribe(64)
		_, stop := start(t, e)
		riding(t, f, e)
		advance(2*time.Second, sub)
		stop()
		var last Tick
		for tk := range sub {
			last = tk
		}
		if last.State != StateFinished || last.Connected {
			t.Fatalf("last tick %+v", last)
		}
		if snap := e.Snapshot(); snap.State != StateFinished || snap.Connected {
			t.Fatalf("snapshot %+v", snap)
		}
	})
}
