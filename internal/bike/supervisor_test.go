package bike

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// fakeSession is a Session whose telemetry and lifetime the test drives.
type fakeSession struct {
	proto Protocol
	max   int
	tel   chan Telemetry
	done  chan struct{}
	once  sync.Once

	mu     sync.Mutex
	writes []int
	closed bool
	err    error
	werr   error // returned by every write (the level is still recorded)
	kill   bool  // a failing write also ends the session (dead link)
	failN  int   // the next failN writes fail with ErrControlRejected (level still recorded)
	echo   bool  // every write reports a sample with the written level
	fixed  int   // every write reports a sample with this level (>0)
	over   bool
}

func newFakeSession(proto Protocol, max int) *fakeSession {
	return &fakeSession{proto: proto, max: max,
		tel: make(chan Telemetry, 16), done: make(chan struct{})}
}

func (f *fakeSession) Protocol() Protocol { return f.proto }
func (f *fakeSession) MaxLevel() int      { return f.max }

func (f *fakeSession) write(level int) (int, error) {
	f.mu.Lock()
	f.writes = append(f.writes, level)
	werr, kill := f.werr, f.kill
	if f.failN > 0 {
		f.failN--
		werr = ErrControlRejected
	}
	if werr == nil && !f.over {
		lvl := 0
		if f.echo {
			lvl = level
		}
		if f.fixed > 0 {
			lvl = f.fixed
		}
		if lvl > 0 {
			select {
			case f.tel <- Telemetry{Level: lvl}:
			default:
			}
		}
	}
	f.mu.Unlock()
	if werr != nil {
		if kill {
			f.end(werr)
		}
		return 0, werr
	}
	return level, nil
}

func (f *fakeSession) SetLevel(_ context.Context, level int) (int, error) { return f.write(level) }
func (f *fakeSession) Resend(_ context.Context, level int) (int, error)   { return f.write(level) }
func (f *fakeSession) Telemetry() <-chan Telemetry                        { return f.tel }
func (f *fakeSession) Done() <-chan struct{}                              { return f.done }

func (f *fakeSession) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeSession) end(err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.err = err
		f.over = true
		close(f.done)
		close(f.tel)
		f.mu.Unlock()
	})
}

// drop simulates a link loss.
func (f *fakeSession) drop() { f.end(errors.New("link lost")) }

func (f *fakeSession) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.end(nil)
	return nil
}

func (f *fakeSession) Writes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

func (f *fakeSession) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

type dialResult struct {
	s   *fakeSession
	err error
}

// fakeDial hands out queued results; once the queue is empty it fails with
// errDialDefault.
type fakeDial struct {
	mu    sync.Mutex
	queue []dialResult
	times []time.Time
}

var errDialDefault = errors.New("no route")

func (d *fakeDial) push(r ...dialResult) {
	d.mu.Lock()
	d.queue = append(d.queue, r...)
	d.mu.Unlock()
}

func (d *fakeDial) dial(ctx context.Context) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.times = append(d.times, time.Now())
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.queue) == 0 {
		return nil, errDialDefault
	}
	r := d.queue[0]
	d.queue = d.queue[1:]
	if r.err != nil {
		return nil, r.err
	}
	return r.s, nil
}

func (d *fakeDial) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.times)
}

func (d *fakeDial) gaps() []time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	var g []time.Duration
	for i := 1; i < len(d.times); i++ {
		g = append(g, d.times[i].Sub(d.times[i-1]))
	}
	return g
}

func drainEvents(ch <-chan Event) []EventKind {
	var k []EventKind
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return k
			}
			k = append(k, e.Kind)
		default:
			return k
		}
	}
}

func TestConnectSendsFloor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		d := &fakeDial{}
		d.push(dialResult{s: s})
		sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: 3})
		if err := sup.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if w := s.Writes(); !slices.Equal(w, []int{3}) {
			t.Fatalf("writes %v", w)
		}
		if c := sup.Caps(); c != (Caps{ProtoFitPlus, 3, 16, true}) {
			t.Fatalf("caps %+v", c)
		}
		if k := drainEvents(sup.Events()); !slices.Equal(k, []EventKind{EventConnected}) {
			t.Fatalf("events %v", k)
		}
		sup.Close(context.Background())
	})
}

func TestSetLevelClamps(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sup := NewSupervisor((&fakeDial{}).dial, SupervisorOptions{})
		defer sup.Close(context.Background())
		for _, c := range []struct{ in, want int }{{0, 1}, {-5, 1}, {99, 16}, {7, 7}} {
			got, err := sup.SetLevel(context.Background(), c.in)
			if err != nil || got != c.want {
				t.Fatalf("SetLevel(%d) = %d, %v; want %d", c.in, got, err, c.want)
			}
		}
	})
}

func FuzzSupervisorNeverBelowFloor(f *testing.F) {
	for _, c := range [][2]int{{0, 0}, {-1, 5}, {1<<31 - 1, 0}, {8, 20}} {
		f.Add(c[0], c[1])
	}
	f.Fuzz(func(t *testing.T, level, min int) {
		synctest.Test(t, func(t *testing.T) {
			lo := func(max int) int {
				l := min
				if l < 1 {
					l = 1
				}
				if l > max {
					l = max
				}
				return l
			}
			check := func(name string, s *fakeSession, max int) {
				for _, w := range s.Writes() {
					if w < lo(max) || w > max {
						t.Fatalf("%s wrote %d outside [%d,%d] (writes %v)", name, w, lo(max), max, s.Writes())
					}
				}
			}
			// The second session has a smaller range: the target must be
			// re-clamped on reconnect.
			s1, s2 := newFakeSession(ProtoFitPlus, 16), newFakeSession(ProtoFitPlus, 10)
			d := &fakeDial{}
			d.push(dialResult{s: s1}, dialResult{s: s2})
			sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: min})
			sup.Connect(context.Background())
			synctest.Wait()
			sup.SetLevel(context.Background(), level)
			synctest.Wait()
			s1.drop()
			synctest.Wait()
			time.Sleep(time.Second)
			synctest.Wait()
			sup.SetLevel(context.Background(), level)
			synctest.Wait()
			sup.Close(context.Background())
			check("s1", s1, 16)
			check("s2", s2, 10)
		})
	})
}

func TestReconnectResendsTarget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s1, s2 := newFakeSession(ProtoFitPlus, 16), newFakeSession(ProtoFitPlus, 16)
		d := &fakeDial{}
		d.push(dialResult{s: s1}, dialResult{s: s2})
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(context.Background())
		synctest.Wait()
		sup.SetLevel(context.Background(), 7)
		time.Sleep(time.Second) // MinWriteInterval
		synctest.Wait()
		if w := s1.Writes(); !slices.Equal(w, []int{1, 7}) {
			t.Fatalf("s1 writes %v", w)
		}
		s1.drop()
		synctest.Wait()
		if d.count() != 1 || sup.Caps().Connected {
			t.Fatalf("dials %d connected %v", d.count(), sup.Caps().Connected)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if d.count() != 2 {
			t.Fatalf("dials %d", d.count())
		}
		if w := s2.Writes(); !slices.Equal(w, []int{7}) {
			t.Fatalf("s2 writes %v", w)
		}
		want := []EventKind{EventConnected, EventDisconnected, EventConnected}
		if k := drainEvents(sup.Events()); !slices.Equal(k, want) {
			t.Fatalf("events %v", k)
		}
		sup.Close(context.Background())
	})
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		for range 7 {
			d.push(dialResult{err: errors.New("down")})
		}
		s := newFakeSession(ProtoFitPlus, 16)
		d.push(dialResult{s: s}, dialResult{s: newFakeSession(ProtoFitPlus, 16)})
		sup := NewSupervisor(d.dial, SupervisorOptions{WatchdogTimeout: 24 * time.Hour})
		sup.Connect(context.Background())
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		sec := time.Second
		want := []time.Duration{sec, 2 * sec, 4 * sec, 8 * sec, 16 * sec, 30 * sec, 30 * sec}
		if g := d.gaps(); !slices.Equal(g, want) {
			t.Fatalf("gaps %v want %v", g, want)
		}
		if !sup.Caps().Connected {
			t.Fatal("not connected after 8th dial")
		}
		// 7 failures emit one Disconnected each, then Connected.
		k := drainEvents(sup.Events())
		if len(k) != 8 || k[7] != EventConnected || k[0] != EventDisconnected {
			t.Fatalf("events %v", k)
		}
		dropAt := time.Now()
		s.drop()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		d.mu.Lock()
		last := d.times[len(d.times)-1]
		d.mu.Unlock()
		if d.count() != 9 || last.Sub(dropAt) != time.Second {
			t.Fatalf("dials %d, redial %v after drop", d.count(), last.Sub(dropAt))
		}
		sup.Close(context.Background())
	})
}

func TestTelemetryAcrossReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s1, s2 := newFakeSession(ProtoFitPlus, 16), newFakeSession(ProtoFitPlus, 16)
		d := &fakeDial{}
		d.push(dialResult{s: s1}, dialResult{s: s2})
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(context.Background())
		synctest.Wait()
		s1.tel <- Telemetry{PowerW: 1}
		s1.tel <- Telemetry{PowerW: 2}
		synctest.Wait()
		s1.drop()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		s2.tel <- Telemetry{PowerW: 3}
		synctest.Wait()
		for want := 1; want <= 3; want++ {
			select {
			case got := <-sup.Telemetry():
				if got.PowerW != float64(want) {
					t.Fatalf("got power %v want %d", got.PowerW, want)
				}
			default:
				t.Fatalf("missing sample %d", want)
			}
		}
		sup.Close(context.Background())
	})
}

func TestCloseSendsFloorAndClosesChannels(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		d := &fakeDial{}
		d.push(dialResult{s: s})
		sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: 2})
		sup.Connect(context.Background())
		synctest.Wait()
		sup.SetLevel(context.Background(), 9)
		time.Sleep(time.Second) // MinWriteInterval
		synctest.Wait()
		if err := sup.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !s.Closed() {
			t.Fatal("session not closed")
		}
		if w := s.Writes(); !slices.Equal(w, []int{2, 9, 2}) {
			t.Fatalf("writes %v, want [2 9 2] (floor written by supervisor before close)", w)
		}
		if _, ok := <-sup.Telemetry(); ok {
			t.Fatal("telemetry open")
		}
		for range sup.Events() { // drains buffered events, must terminate
		}
		if _, err := sup.SetLevel(context.Background(), 5); !errors.Is(err, ErrClosed) {
			t.Fatalf("SetLevel after Close: %v", err)
		}
		if err := sup.Close(context.Background()); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if sup.Caps().Connected {
			t.Fatal("still connected")
		}
	})
}

func TestCloseDuringBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(context.Background())
		time.Sleep(5 * time.Second)
		synctest.Wait()
		n := d.count()
		if err := sup.Close(context.Background()); !errors.Is(err, ErrNoSession) {
			t.Fatalf("Close during backoff = %v, want ErrNoSession", err)
		}
		time.Sleep(time.Minute)
		if d.count() != n {
			t.Fatalf("dialed after Close: %d -> %d", n, d.count())
		}
	})
}

func TestCloseBeforeConnect(t *testing.T) {
	t.Parallel()
	sup := NewSupervisor((&fakeDial{}).dial, SupervisorOptions{})
	if err := sup.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-sup.Telemetry(); ok {
		t.Fatal("telemetry open")
	}
	if _, ok := <-sup.Events(); ok {
		t.Fatal("events open")
	}
	if err := sup.Connect(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect after Close: %v", err)
	}
}

func TestRejectedWritesDoNotCauseReconnectStorm(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFTMS, 16)
		s.werr = ErrControlRejected
		d := &fakeDial{}
		d.push(dialResult{s: s})
		sup := NewSupervisor(d.dial, SupervisorOptions{WatchdogTimeout: 24 * time.Hour})
		sup.Connect(context.Background())
		time.Sleep(time.Minute)
		synctest.Wait()
		if d.count() != 1 || s.Closed() || !sup.Caps().Connected {
			t.Fatalf("dials %d closed %v connected %v", d.count(), s.Closed(), sup.Caps().Connected)
		}
		// The rejected connect write was retried once after ConfirmWindow.
		if w := s.Writes(); !slices.Equal(w, []int{1, 1}) {
			t.Fatalf("writes %v", w)
		}
		sup.SetLevel(context.Background(), 5)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if w := s.Writes(); !slices.Equal(w, []int{1, 1, 5, 5}) {
			t.Fatalf("writes %v", w)
		}
		var kinds []EventKind
		for _, e := range drainEventsFull(sup.Events()) {
			kinds = append(kinds, e.Kind)
			if e.Kind == EventLevelUnconfirmed && !errors.Is(e.Err, ErrControlRejected) {
				t.Fatalf("event %+v", e)
			}
		}
		want := []EventKind{EventConnected, EventLevelUnconfirmed, EventLevelUnconfirmed}
		if !slices.Equal(kinds, want) {
			t.Fatalf("events %v want %v", kinds, want)
		}
		sup.Close(context.Background())
	})
}

func TestFirstWriteKillsLinkKeepsBackingOff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		for range 5 {
			s := newFakeSession(ProtoFitPlus, 16)
			s.werr, s.kill = errors.New("link died"), true
			d.push(dialResult{s: s})
		}
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(context.Background())
		time.Sleep(time.Minute)
		synctest.Wait()
		sec := time.Second
		g := d.gaps()
		if len(g) < 4 || !slices.Equal(g[:4], []time.Duration{sec, 2 * sec, 4 * sec, 8 * sec}) {
			t.Fatalf("gaps %v", g)
		}
		sup.Close(context.Background())
	})
}

func TestParentContextCancelStopsSupervisor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		d := &fakeDial{}
		d.push(dialResult{s: s})
		ctx, cancel := context.WithCancel(context.Background())
		sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: 2})
		sup.Connect(ctx)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !s.Closed() {
			t.Fatal("session not closed")
		}
		if w := s.Writes(); w[len(w)-1] != 2 {
			t.Fatalf("writes %v, want floor last", w)
		}
		if _, err := sup.SetLevel(context.Background(), 4); !errors.Is(err, ErrClosed) {
			t.Fatalf("SetLevel: %v", err)
		}
		if err := sup.Connect(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("Connect: %v", err)
		}
		if _, ok := <-sup.Telemetry(); ok {
			t.Fatal("telemetry open")
		}
		if err := sup.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func drainEventsFull(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}

func countKind(evs []Event, k EventKind) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func ctxb() context.Context { return context.Background() }

func start(t *testing.T, s *fakeSession, o SupervisorOptions) *Supervisor {
	t.Helper()
	d := &fakeDial{}
	d.push(dialResult{s: s})
	sup := NewSupervisor(d.dial, o)
	if err := sup.Connect(ctxb()); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	return sup
}

func wantWrites(t *testing.T, s *fakeSession, want ...int) {
	t.Helper()
	if w := s.Writes(); !slices.Equal(w, want) {
		t.Fatalf("at %v writes %v want %v", time.Now().Format("05.000"), w, want)
	}
}

func TestRateLimitCoalesces(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{})
		for _, l := range []int{5, 6, 7} {
			sup.SetLevel(ctxb(), l)
		}
		synctest.Wait()
		wantWrites(t, s, 1)
		time.Sleep(time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 7)
		time.Sleep(500 * time.Millisecond)
		sup.SetLevel(ctxb(), 8)
		synctest.Wait()
		time.Sleep(490 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 7)
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 7, 8)
		sup.Close(ctxb())
	})
}

func TestConfirmSatisfied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		s.echo = true
		sup := start(t, s, SupervisorOptions{})
		sup.SetLevel(ctxb(), 5)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 5)
		if n := countKind(drainEventsFull(sup.Events()), EventLevelUnconfirmed); n != 0 {
			t.Fatalf("%d unconfirmed events", n)
		}
		sup.Close(ctxb())
	})
}

func TestConfirmRetryOnceThenAlert(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		s.fixed = 1
		sup := start(t, s, SupervisorOptions{})
		sup.SetLevel(ctxb(), 5)
		time.Sleep(2900 * time.Millisecond) // t=2.9s
		synctest.Wait()
		wantWrites(t, s, 1, 5)
		time.Sleep(100 * time.Millisecond) // t=3s
		synctest.Wait()
		wantWrites(t, s, 1, 5, 5)
		time.Sleep(1900 * time.Millisecond) // t=4.9s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventLevelUnconfirmed); n != 0 {
			t.Fatalf("early alert (%d)", n)
		}
		time.Sleep(100 * time.Millisecond) // t=5s
		synctest.Wait()
		var alerts []Event
		for _, e := range drainEventsFull(sup.Events()) {
			if e.Kind == EventLevelUnconfirmed {
				alerts = append(alerts, e)
			}
		}
		if len(alerts) != 1 || alerts[0].Want != 5 || alerts[0].Got != 1 {
			t.Fatalf("alerts %+v", alerts)
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 5, 5)
		if n := countKind(drainEventsFull(sup.Events()), EventLevelUnconfirmed); n != 0 {
			t.Fatalf("repeat alert (%d)", n)
		}
		sup.Close(ctxb())
	})
}

func TestConfirmSkippedWithoutReportedLevel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{})
		s.tel <- Telemetry{Level: 0, PowerW: 50}
		sup.SetLevel(ctxb(), 5)
		time.Sleep(20 * time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 5)
		if n := countKind(drainEventsFull(sup.Events()), EventLevelUnconfirmed); n != 0 {
			t.Fatalf("%d alerts", n)
		}
		sup.Close(ctxb())
	})
}

func TestWatchdogTripsOncePerEpisode(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{})
		sup.SetLevel(ctxb(), 9)
		for i := 0; i <= 10; i++ {
			s.tel <- Telemetry{Cadence: 80}
			if i < 10 {
				time.Sleep(time.Second)
			}
		}
		s.tel <- Telemetry{Cadence: 0}
		synctest.Wait()
		wantWrites(t, s, 1, 9)
		time.Sleep(29900 * time.Millisecond) // t=39.9s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 0 {
			t.Fatalf("early watchdog (%d)", n)
		}
		time.Sleep(100 * time.Millisecond) // t=40s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 1 {
			t.Fatalf("watchdog events %d", n)
		}
		wantWrites(t, s, 1, 9, 1)
		time.Sleep(60 * time.Second) // t=100s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 0 {
			t.Fatalf("watchdog repeated (%d)", n)
		}
		wantWrites(t, s, 1, 9, 1)
		s.tel <- Telemetry{Cadence: 80}
		time.Sleep(time.Second)
		s.tel <- Telemetry{Cadence: 80}
		sup.SetLevel(ctxb(), 9)
		s.tel <- Telemetry{Cadence: 0}
		synctest.Wait()
		time.Sleep(29900 * time.Millisecond) // t=130.9s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 0 {
			t.Fatalf("early second watchdog (%d)", n)
		}
		time.Sleep(100 * time.Millisecond) // t=131s
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 1 {
			t.Fatalf("second watchdog events %d", n)
		}
		wantWrites(t, s, 1, 9, 1, 9, 1)
		sup.Close(ctxb())
	})
}

func TestWatchdogNoTelemetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{})
		time.Sleep(29900 * time.Millisecond)
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 0 {
			t.Fatalf("early watchdog (%d)", n)
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 1 {
			t.Fatalf("watchdog events %d", n)
		}
		sup.Close(ctxb())
	})
}

func TestWatchdogWhileDisconnectedFloorsOnReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: 2})
		sup.Connect(ctxb())
		sup.SetLevel(ctxb(), 9)
		time.Sleep(30500 * time.Millisecond)
		synctest.Wait()
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 1 {
			t.Fatalf("watchdog events %d", n)
		}
		s := newFakeSession(ProtoFitPlus, 16)
		d.push(dialResult{s: s}) // next redial lands at t=31s
		time.Sleep(time.Second)
		synctest.Wait()
		wantWrites(t, s, 2)
		sup.Close(ctxb())
	})
}

func TestRejectedTargetIsRetriedWithoutTargetChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFTMS, 16)
		sup := start(t, s, SupervisorOptions{WatchdogTimeout: 24 * time.Hour})
		s.mu.Lock()
		s.failN = 1
		s.mu.Unlock()
		sup.SetLevel(ctxb(), 5)
		time.Sleep(time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 5)
		time.Sleep(2 * time.Second) // ConfirmWindow after the failed write
		synctest.Wait()
		wantWrites(t, s, 1, 5, 5)
		time.Sleep(time.Minute)
		synctest.Wait()
		wantWrites(t, s, 1, 5, 5)
		if n := countKind(drainEventsFull(sup.Events()), EventLevelUnconfirmed); n != 0 {
			t.Fatalf("%d alerts after a successful retry", n)
		}
		sup.Close(ctxb())
	})
}

func TestBackoffResetsAfterAnySuccessfulWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		for range 3 {
			d.push(dialResult{err: errors.New("down")})
		}
		s := newFakeSession(ProtoFitPlus, 16)
		s.failN = 1 // first write fails but the link stays up
		d.push(dialResult{s: s}, dialResult{s: newFakeSession(ProtoFitPlus, 16)})
		sup := NewSupervisor(d.dial, SupervisorOptions{WatchdogTimeout: 24 * time.Hour})
		sup.Connect(ctxb())
		time.Sleep(20 * time.Second) // dials at 0,1,3,7; retry write succeeds at 9s
		synctest.Wait()
		dropAt := time.Now()
		s.drop()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		d.mu.Lock()
		times := slices.Clone(d.times)
		d.mu.Unlock()
		if len(times) != 5 || times[4].Sub(dropAt) != time.Second {
			t.Fatalf("dial times %v, want redial 1s after the drop", times)
		}
		sup.Close(ctxb())
	})
}

func TestCloseFloorClampedToSessionMax(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 10)
		ctx, cancel := context.WithCancel(ctxb())
		dial := func(ctx context.Context) (Session, error) {
			<-ctx.Done() // the session arrives after supervision was cancelled
			return s, nil
		}
		sup := NewSupervisor(dial, SupervisorOptions{MinLevel: 12})
		sup.Connect(ctx)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if w := s.Writes(); len(w) != 1 || w[0] != 10 {
			t.Fatalf("writes %v, want floor clamped to session max [10]", w)
		}
		sup.Close(ctxb())
	})
}

func TestWatchdogFloorNotReplacedByRacingSetLevel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{})
		time.Sleep(5 * time.Second)
		synctest.Wait()
		// The trip and a SetLevel land before the serve loop runs.
		sup.tripWatchdog()
		sup.SetLevel(ctxb(), 9)
		synctest.Wait()
		wantWrites(t, s, 1, 1)
		time.Sleep(990 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 1)
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 1, 9)
		sup.Close(ctxb())
	})
}

func TestWatchdogBypassesWriteGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		sup := start(t, s, SupervisorOptions{WatchdogTimeout: 500 * time.Millisecond})
		sup.SetLevel(ctxb(), 9) // gated until 1s
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 1)
		if n := countKind(drainEventsFull(sup.Events()), EventWatchdog); n != 1 {
			t.Fatalf("watchdog events %d", n)
		}
		sup.Close(ctxb())
	})
}

func TestNewWriteCancelsPendingConfirmation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		s.fixed = 1
		sup := start(t, s, SupervisorOptions{})
		sup.SetLevel(ctxb(), 5) // written at 1s, retry would be due at 3s
		time.Sleep(1500 * time.Millisecond)
		sup.SetLevel(ctxb(), 6) // written at 2s
		time.Sleep(10 * time.Second)
		synctest.Wait()
		wantWrites(t, s, 1, 5, 6, 6)
		var alerts []Event
		for _, e := range drainEventsFull(sup.Events()) {
			if e.Kind == EventLevelUnconfirmed {
				alerts = append(alerts, e)
			}
		}
		if len(alerts) != 1 || alerts[0].Want != 6 {
			t.Fatalf("alerts %+v", alerts)
		}
		sup.Close(ctxb())
	})
}

func TestConfirmRetryRespectsWriteGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newFakeSession(ProtoFitPlus, 16)
		s.fixed = 1
		sup := start(t, s, SupervisorOptions{ConfirmWindow: 100 * time.Millisecond})
		sup.SetLevel(ctxb(), 5) // written at 1s
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		wantWrites(t, s, 1, 5)
		time.Sleep(500 * time.Millisecond) // 2s: one MinWriteInterval after the write
		synctest.Wait()
		wantWrites(t, s, 1, 5, 5)
		sup.Close(ctxb())
	})
}

func TestCloseReportsFloorOutcome(t *testing.T) {
	t.Parallel()
	errRejected := errors.New("floor rejected")
	for name, tc := range map[string]struct {
		werr    error
		cancel  bool // end supervision by cancelling the Connect ctx
		want    error
		wantNil bool
	}{
		"accepted":         {wantNil: true},
		"rejected":         {werr: errRejected, want: errRejected},
		"link gone":        {werr: ErrClosed, want: ErrNoSession},
		"accepted, cancel": {cancel: true, wantNil: true},
		"rejected, cancel": {werr: errRejected, cancel: true, want: errRejected},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := newFakeSession(ProtoFitPlus, 16)
				d := &fakeDial{}
				d.push(dialResult{s: s})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				sup := NewSupervisor(d.dial, SupervisorOptions{})
				sup.Connect(ctx)
				synctest.Wait()
				s.mu.Lock()
				s.werr = tc.werr
				s.mu.Unlock()
				if tc.cancel {
					cancel()
					synctest.Wait()
				}
				err := sup.Close(context.Background())
				if tc.wantNil && err != nil || !tc.wantNil && !errors.Is(err, tc.want) {
					t.Fatalf("Close = %v, want %v", err, tc.want)
				}
				if again := sup.Close(context.Background()); (again == nil) != (err == nil) {
					t.Fatalf("second Close = %v, first %v", again, err)
				}
			})
		})
	}
}

func TestCloseAfterCancelDuringBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := &fakeDial{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(ctx)
		time.Sleep(3 * time.Second)
		cancel()
		synctest.Wait()
		if err := sup.Close(context.Background()); !errors.Is(err, ErrNoSession) {
			t.Fatalf("Close = %v, want ErrNoSession", err)
		}
	})
}
