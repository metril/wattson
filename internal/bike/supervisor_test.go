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
	floor int
	tel   chan Telemetry
	done  chan struct{}
	once  sync.Once

	mu     sync.Mutex
	writes []int
	closed bool
	err    error
}

func newFakeSession(proto Protocol, max, floor int) *fakeSession {
	return &fakeSession{proto: proto, max: max, floor: floor,
		tel: make(chan Telemetry, 16), done: make(chan struct{})}
}

func (f *fakeSession) Protocol() Protocol { return f.proto }
func (f *fakeSession) MaxLevel() int      { return f.max }

func (f *fakeSession) write(level int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, level)
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
		f.mu.Unlock()
		close(f.done)
		close(f.tel)
	})
}

// drop simulates a link loss.
func (f *fakeSession) drop() { f.end(errors.New("link lost")) }

func (f *fakeSession) Close(context.Context) error {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		f.writes = append(f.writes, f.floor)
	}
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
		s := newFakeSession(ProtoFitPlus, 16, 3)
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
			lo := min
			if lo < 1 {
				lo = 1
			}
			if lo > 16 {
				lo = 16
			}
			s := newFakeSession(ProtoFitPlus, 16, lo)
			d := &fakeDial{}
			d.push(dialResult{s: s}, dialResult{s: newFakeSession(ProtoFitPlus, 16, lo)})
			sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: min})
			sup.Connect(context.Background())
			synctest.Wait()
			sup.SetLevel(context.Background(), level)
			synctest.Wait()
			sup.Close(context.Background())
			for _, w := range s.Writes() {
				if w < lo || w > 16 {
					t.Fatalf("wrote %d outside [%d,16] (writes %v)", w, lo, s.Writes())
				}
			}
		})
	})
}

func TestReconnectResendsTarget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s1, s2 := newFakeSession(ProtoFitPlus, 16, 1), newFakeSession(ProtoFitPlus, 16, 1)
		d := &fakeDial{}
		d.push(dialResult{s: s1}, dialResult{s: s2})
		sup := NewSupervisor(d.dial, SupervisorOptions{})
		sup.Connect(context.Background())
		synctest.Wait()
		sup.SetLevel(context.Background(), 7)
		synctest.Wait()
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
		s := newFakeSession(ProtoFitPlus, 16, 1)
		d.push(dialResult{s: s}, dialResult{s: newFakeSession(ProtoFitPlus, 16, 1)})
		sup := NewSupervisor(d.dial, SupervisorOptions{})
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
		s1, s2 := newFakeSession(ProtoFitPlus, 16, 1), newFakeSession(ProtoFitPlus, 16, 1)
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
		s := newFakeSession(ProtoFitPlus, 16, 2)
		d := &fakeDial{}
		d.push(dialResult{s: s})
		sup := NewSupervisor(d.dial, SupervisorOptions{MinLevel: 2})
		sup.Connect(context.Background())
		synctest.Wait()
		sup.SetLevel(context.Background(), 9)
		synctest.Wait()
		if err := sup.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !s.Closed() {
			t.Fatal("session not closed")
		}
		if w := s.Writes(); w[len(w)-1] != 2 {
			t.Fatalf("last write %v, want floor 2", w)
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
		if err := sup.Close(context.Background()); err != nil {
			t.Fatal(err)
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
