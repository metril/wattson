package ridectl

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

// memModels is an in-memory PowerModelStore.
type memModels struct {
	mu   sync.Mutex
	rows map[string][]byte
	puts int
}

func (s *memModels) PowerModel(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.rows[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return b, nil
}

func (s *memModels) PutPowerModel(_ context.Context, key string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string][]byte{}
	}
	s.rows[key] = append([]byte(nil), b...)
	s.puts++
	return nil
}

func (s *memModels) DeletePowerModel(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, key)
	return nil
}

func (s *memModels) state(t *testing.T, key string) (bike.PowerModelState, bool) {
	t.Helper()
	s.mu.Lock()
	b, ok := s.rows[key]
	s.mu.Unlock()
	var st bike.PowerModelState
	if ok {
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
	}
	return st, ok
}

func TestBikeKey(t *testing.T) {
	t.Parallel()
	mk := func(f func(*settings.Bike)) settings.Settings {
		s := settings.Defaults()
		f(&s.Bike)
		return s
	}
	for name, tc := range map[string]struct {
		s    settings.Settings
		want string
	}{
		"sim":       {mk(func(b *settings.Bike) { b.Driver = "sim" }), "sim"},
		"fitplus":   {mk(func(b *settings.Bike) { b.Driver = "fitplus"; b.Address = "aa:bb:cc:dd:ee:ff" }), "ble:AA:BB:CC:DD:EE:FF"},
		"auto":      {mk(func(b *settings.Bike) { b.Driver = "auto"; b.Address = " c0:ff:ee:00:00:01 " }), "ble:C0:FF:EE:00:00:01"},
		"ha":        {mk(func(b *settings.Bike) { b.Driver = "ha"; b.HAResistanceEntity = "number.bike_level" }), "ha:number.bike_level"},
		"ftms-none": {mk(func(b *settings.Bike) { b.Driver = "ftms" }), "ble:"},
	} {
		if got := BikeKey(tc.s); got != tc.want {
			t.Errorf("%s: BikeKey = %q, want %q", name, got, tc.want)
		}
	}
}

// drainTicks reads every tick currently queued on ch.
func drainTicks(ch <-chan Event) []engine.Tick {
	var out []engine.Tick
	for {
		select {
		case ev := <-ch:
			if ev.Type == "tick" {
				out = append(out, *ev.Tick)
			}
		default:
			return out
		}
	}
}

func TestPowerModelPersisted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pm := &memModels{}
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{PowerModels: pm})
		defer m.Close(context.Background())
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready")
		info, err := m.PowerModelInfo(t.Context())
		if err != nil || !info.Seeded || info.Samples != 0 || info.BikeKey != "sim" || len(info.WattsAt80) != 16 || math.Abs(info.WattsAt80[0]-50) > 1e-9 {
			t.Fatalf("fresh info = %+v, %v", info, err)
		}
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStart}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		for i := range 6 {
			time.Sleep(10 * time.Second)
			if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdSetLevel, Value: 3 + 2*i}); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStop}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready") // the stopped engine has returned and saved
		st, ok := pm.state(t, "sim")
		if !ok || st.N <= 0 {
			t.Fatalf("stored model = %+v ok=%v, want N > 0", st, ok)
		}
		if err := m.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "idle")
		if info, _ := m.PowerModelInfo(t.Context()); info.Samples != st.N || info.Seeded {
			t.Errorf("idle info = %+v, want samples %d", info, st.N)
		}
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready")
		if info, _ := m.PowerModelInfo(t.Context()); info.Samples != st.N {
			t.Errorf("reconnected samples = %d, want %d", info.Samples, st.N)
		}
		// Reset drops the stored row and the live model.
		if err := m.ResetPowerModel(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, ok := pm.state(t, "sim"); ok {
			t.Error("stored row survived ResetPowerModel")
		}
		if info, _ := m.PowerModelInfo(t.Context()); !info.Seeded || info.Samples != 0 {
			t.Errorf("after reset info = %+v", info)
		}
	})
}

func TestPowerModelUnparsableFallsBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pm := &memModels{rows: map[string][]byte{"sim": []byte("not json")}}
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{PowerModels: pm})
		defer m.Close(context.Background())
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready")
		if info, _ := m.PowerModelInfo(t.Context()); !info.Seeded {
			t.Errorf("info = %+v, want seeded", info)
		}
	})
}

func TestMultiplierApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := testRider
		r.PowerMultiplier = 1.1
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		if _, err := m.Connect(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready")
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStart}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		time.Sleep(5 * time.Second)
		tk := m.Snapshot()
		if tk.Level != 5 || math.Abs(tk.PowerW-143) > 0.5 {
			t.Errorf("level %d power %v, want L5 143 W (130 W x 1.1)", tk.Level, tk.PowerW)
		}
	})
}

// gatedModels blocks PutPowerModel until released, after announcing itself.
type gatedModels struct {
	memModels
	inPut   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedModels) PutPowerModel(ctx context.Context, key string, b []byte) error {
	g.once.Do(func() { close(g.inPut) })
	<-g.release
	return g.memModels.PutPowerModel(ctx, key, b)
}

// A reset that arrives while a save is in flight must not be undone by the
// save's stale Put.
func TestResetDuringSaveDoesNotResurrect(t *testing.T) {
	t.Parallel()
	pm := &gatedModels{inPut: make(chan struct{}), release: make(chan struct{})}
	m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{PowerModels: pm})
	defer m.Close(context.Background())
	if _, err := m.Connect(t.Context(), testRider); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
	m.mu.Lock()
	c := m.cur
	m.mu.Unlock()
	for i := range 5 {
		if !c.model.Observe(3+i, 80, 80*(0.375+0.25*float64(3+i))) {
			t.Fatal("sample rejected")
		}
	}
	saved := make(chan struct{})
	go func() { defer close(saved); m.saveModel(c) }()
	<-pm.inPut // the save has read the learned state and is inside Put
	reset := make(chan error, 1)
	go func() { reset <- m.ResetPowerModel(t.Context()) }()
	var rerr error
	done := false
	select {
	case rerr, done = <-reset: // not serialised: the reset overtook the save
	case <-time.After(20 * time.Millisecond):
	}
	close(pm.release)
	<-saved
	if !done {
		rerr = <-reset
	}
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, ok := pm.state(t, "sim"); ok {
		t.Error("the stale save resurrected the row after ResetPowerModel")
	}
}
