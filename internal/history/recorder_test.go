package history_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/store"
)

// memStore is an in-memory RideStore.
type memStore struct {
	mu          sync.Mutex
	rides       map[int64]store.Ride
	samples     map[int64][]store.Sample
	nextID      int64
	appendFails int // next N AppendSamples calls fail
	created     []store.Ride
	finishes    []finishCall
	deletes     int
}

type finishCall struct {
	id    int64
	endAt time.Time
	sum   store.Summary
	n     int
}

func newMem() *memStore {
	return &memStore{rides: map[int64]store.Ride{}, samples: map[int64][]store.Sample{}}
}

func (m *memStore) CreateRide(_ context.Context, r store.Ride) (store.Ride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	r.ID = m.nextID
	m.rides[r.ID] = r
	m.created = append(m.created, r)
	return r, nil
}

func (m *memStore) AppendSamples(_ context.Context, id int64, ss []store.Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendFails > 0 {
		m.appendFails--
		return errors.New("disk full")
	}
	have := map[int]bool{}
	for _, x := range m.samples[id] {
		have[x.Seq] = true
	}
	for _, x := range ss {
		if !have[x.Seq] {
			m.samples[id] = append(m.samples[id], x)
		}
	}
	return nil
}

func (m *memStore) Samples(_ context.Context, id int64) ([]store.Sample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Sample(nil), m.samples[id]...), nil
}

func (m *memStore) FinishRide(_ context.Context, id int64, end time.Time, sum store.Summary, n int) (store.Ride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishes = append(m.finishes, finishCall{id, end, sum, n})
	r := m.rides[id]
	r.EndedAt, r.Summary, r.SampleCount = &end, sum, n
	m.rides[id] = r
	return r, nil
}

func (m *memStore) DeleteRide(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes++
	delete(m.rides, id)
	delete(m.samples, id)
	return nil
}

func (m *memStore) count(id int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.samples[id])
}

// harness runs a Recorder in the current synctest bubble.
type harness struct {
	mem      *memStore
	ev       chan ridectl.Event
	cancel   context.CancelFunc
	done     chan error
	mu       sync.Mutex
	finished []store.Ride
}

func start(d history.RecorderDeps) *harness {
	h := &harness{mem: newMem(), ev: make(chan ridectl.Event, 1024), done: make(chan error, 1)}
	d.Store = h.mem
	d.OnFinished = func(_ context.Context, r store.Ride) {
		h.mu.Lock()
		h.finished = append(h.finished, r)
		h.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	rec := history.NewRecorder(d)
	go func() { h.done <- rec.Run(ctx, h.ev) }()
	return h
}

// stop cancels the recorder and waits for Run so the bubble can end.
func (h *harness) stop() error {
	h.cancel()
	return <-h.done
}

func (h *harness) status(rider int64, state string) {
	h.ev <- ridectl.Event{Type: "status", Status: &ridectl.Status{RiderID: rider, State: state}}
}

func (h *harness) tick(tk engine.Tick) { h.ev <- ridectl.Event{Type: "tick", Tick: &tk} }

func riding(i int) engine.Tick {
	return engine.Tick{At: t0.Add(time.Duration(i) * time.Second), State: engine.StateRiding, Mode: "free",
		ElapsedS: float64(i + 1), DistanceM: float64(10 * (i + 1)), PowerW: 200, CadenceRpm: 80}
}

// ride sends status ready then n riding ticks at virtual 0.5+i s, leaving the
// clock at n-0.5 s.
func (h *harness) ride(n int) {
	h.status(1, "ready")
	time.Sleep(500 * time.Millisecond)
	for i := range n {
		h.tick(riding(i))
		if i < n-1 {
			time.Sleep(time.Second)
		}
	}
	synctest.Wait()
}

func (h *harness) finishedRides() []store.Ride {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Ride(nil), h.finished...)
}

func TestRecorderBatchesEvery10s(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.ride(10) // last tick at 9.5 s
		time.Sleep(400 * time.Millisecond)
		synctest.Wait()
		if len(h.mem.created) != 1 || h.mem.count(1) != 0 {
			t.Fatalf("at 9.9 s: rides=%d samples=%d", len(h.mem.created), h.mem.count(1))
		}
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 10 {
			t.Fatalf("at 10.1 s: %d samples", len(ss))
		}
		for i, s := range ss {
			if s.Seq != i {
				t.Fatalf("seq[%d] = %d", i, s.Seq)
			}
		}
	})
}

func TestRecorderDedupesSubSecond(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{FlushEvery: time.Second})
		defer h.stop()
		h.status(1, "ready")
		for _, e := range []float64{1.0, 1.02, 1.5, 2.0} {
			tk := riding(0)
			tk.ElapsedS = e
			h.tick(tk)
		}
		synctest.Wait()
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		if n := h.mem.count(1); n != 2 {
			t.Errorf("samples = %d, want 2", n)
		}
	})
}

func TestRecorderFinalizeOnFinished(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.ride(30)
		fin := riding(29)
		fin.State, fin.ElapsedS = engine.StateFinished, 30
		h.tick(fin)
		synctest.Wait()
		if len(h.mem.finishes) != 1 {
			t.Fatalf("finishes = %d", len(h.mem.finishes))
		}
		f := h.mem.finishes[0]
		if f.n != 30 || f.sum.AvgPowerW != 200 || f.sum.DurationS != 30 || !f.endAt.Equal(t0.Add(29*time.Second)) {
			t.Errorf("finish = %+v", f)
		}
		got := h.finishedRides()
		if len(got) != 1 || got[0].ID != 1 || got[0].SampleCount != 30 {
			t.Errorf("OnFinished = %+v", got)
		}
	})
}

func TestRecorderSkipsPaused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.ride(10)
		for i := range 5 {
			tk := riding(9)
			tk.State, tk.At = engine.StatePaused, t0.Add(time.Duration(10+i)*time.Second)
			h.tick(tk)
		}
		for i := 10; i < 20; i++ {
			tk := riding(i)
			tk.At = t0.Add(time.Duration(i+5) * time.Second)
			h.tick(tk)
		}
		fin := riding(19)
		fin.State, fin.ElapsedS = engine.StateFinished, 20
		h.tick(fin)
		synctest.Wait()
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 20 {
			t.Fatalf("samples = %d", len(ss))
		}
		for i, s := range ss {
			if s.Seq != i {
				t.Fatalf("seq[%d] = %d", i, s.Seq)
			}
		}
	})
}

func TestRecorderDiscardsShort(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.ride(5)
		fin := riding(4)
		fin.State = engine.StateFinished
		h.tick(fin)
		synctest.Wait()
		if h.mem.deletes != 1 || len(h.mem.finishes) != 0 || len(h.finishedRides()) != 0 {
			t.Errorf("deletes=%d finishes=%d onFinished=%d", h.mem.deletes, len(h.mem.finishes), len(h.finishedRides()))
		}
	})
}

func TestRecorderFinalizeOnIdle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.ride(15)
		h.status(0, "idle")
		synctest.Wait()
		if len(h.mem.finishes) != 1 || h.mem.finishes[0].n != 15 {
			t.Errorf("finishes = %+v", h.mem.finishes)
		}
	})
}

func TestRecorderCtxCancelFinalizes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		h.ride(15)
		if err := h.stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
		if len(h.mem.finishes) != 1 || h.mem.finishes[0].n != 15 {
			t.Errorf("finishes = %+v", h.mem.finishes)
		}
	})
}

func TestRecorderClosedChannelFinalizes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		h.ride(15)
		close(h.ev)
		if err := <-h.done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if len(h.mem.finishes) != 1 || h.mem.finishes[0].n != 15 {
			t.Errorf("finishes = %+v", h.mem.finishes)
		}
	})
}

func TestRecorderRoutePosition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		prof, err := route.Process(route.Synthetic([]route.Segment{{LengthM: 1000, GradePct: 0}}))
		if err != nil {
			t.Fatal(err)
		}
		var asked int64
		h := start(history.RecorderDeps{FlushEvery: time.Second,
			RouteInfo: func(_ context.Context, id int64) (string, *route.Profile, error) {
				asked = id
				return "Ramp", prof, nil
			}})
		defer h.stop()
		h.status(1, "ready")
		tk := riding(0)
		tk.Mode, tk.RouteID, tk.DistanceM = "route", 3, 500
		h.tick(tk)
		synctest.Wait()
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		if asked != 3 || len(h.mem.created) != 1 || h.mem.created[0].RouteName != "Ramp" || h.mem.created[0].Mode != "route" || h.mem.created[0].RouteID != 3 {
			t.Fatalf("asked=%d created=%+v", asked, h.mem.created)
		}
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 1 || !near(ss[0].Lat, 0, 1e-6) || !near(ss[0].Lon, 0.0044966, 1e-5) || !near(ss[0].EleM, 100, 0.01) {
			t.Errorf("sample = %+v", ss)
		}
	})
}

func TestRecorderFreeNoPosition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{FlushEvery: time.Second})
		defer h.stop()
		h.status(1, "ready")
		h.tick(riding(0))
		synctest.Wait()
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 1 || !math.IsNaN(ss[0].Lat) || !math.IsNaN(ss[0].Lon) || !math.IsNaN(ss[0].EleM) {
			t.Errorf("sample = %+v", ss)
		}
	})
}

func TestRecorderRouteInfoErrorDropsRouteLink(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{
			RouteInfo: func(context.Context, int64) (string, *route.Profile, error) { return "", nil, errors.New("gone") }})
		defer h.stop()
		h.status(1, "ready")
		tk := riding(0)
		tk.Mode, tk.RouteID = "route", 9
		h.tick(tk)
		synctest.Wait()
		if len(h.mem.created) != 1 || h.mem.created[0].RouteID != 0 || h.mem.created[0].RouteName != "" {
			t.Errorf("created = %+v", h.mem.created)
		}
	})
}

func TestRecorderFlushErrorRetries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		h.mem.appendFails = 1
		h.ride(20) // last tick at 19.5 s
		time.Sleep(600 * time.Millisecond)
		synctest.Wait()
		if n := h.mem.count(1); n != 20 {
			t.Errorf("samples at 20.1 s = %d, want 20", n)
		}
	})
}

func TestRecorderNoRider(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{})
		defer h.stop()
		for i := range 3 {
			h.tick(riding(i))
		}
		synctest.Wait()
		if len(h.mem.created) != 0 {
			t.Errorf("created = %+v", h.mem.created)
		}
	})
}

func TestRecorderHeartRate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{FlushEvery: time.Second})
		defer h.stop()
		h.status(1, "ready")
		tk := riding(0)
		tk.HeartRateBpm = 150
		h.tick(tk)
		synctest.Wait()
		time.Sleep(1100 * time.Millisecond)
		synctest.Wait()
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 1 || ss[0].HRBpm != 150 {
			t.Errorf("sample = %+v", ss)
		}
	})
}

func TestRecorderBufferCap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := start(history.RecorderDeps{FlushEvery: time.Second})
		defer h.stop()
		h.mem.appendFails = 100000
		h.status(1, "ready")
		for i := range 700 {
			h.tick(riding(i))
			time.Sleep(time.Second)
		}
		synctest.Wait()
		h.mem.mu.Lock()
		h.mem.appendFails = 0
		h.mem.mu.Unlock()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		ss, _ := h.mem.Samples(t.Context(), 1)
		if len(ss) != 600 || ss[0].Seq != 100 || ss[599].Seq != 699 {
			t.Errorf("stored %d samples, want the newest 600 (seq 100..699)", len(ss))
		}
	})
}
