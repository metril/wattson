package ridectl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/settings"
)

func start(t *testing.T, m *Manager) {
	t.Helper()
	if _, err := m.Connect(t.Context(), testRider); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
}

func ride(t *testing.T, m *Manager) {
	t.Helper()
	start(t, m)
	if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStart}); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "riding")
}

func TestConnectSimReachesReady(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		st, err := m.Connect(t.Context(), testRider)
		if err != nil || st.State != "connecting" {
			t.Fatalf("Connect = %+v, %v", st, err)
		}
		time.Sleep(2 * time.Second)
		st = m.Status()
		if st.State != "ready" || st.Protocol != "sim" || !st.Connected || st.Driver != "sim" || st.RiderID != testRider.ID {
			t.Fatalf("status = %+v", st)
		}
		if !m.RiderInUse(testRider.ID) || m.RiderInUse(99) {
			t.Error("RiderInUse wrong")
		}
	})
}

func TestConnectBusy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		if _, err := m.Connect(t.Context(), testRider); !errors.Is(err, ErrBusy) {
			t.Fatalf("second Connect = %v, want ErrBusy", err)
		}
	})
}

func TestRideLifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &simRec{}
		m := newMgr(rec, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		ride(t, m)
		time.Sleep(3 * time.Second)
		tk := m.Snapshot()
		if tk.State != engine.StateRiding || tk.PowerW != sim.Power(5, 80) || tk.PowerW != 130 {
			t.Fatalf("tick = %+v", tk)
		}
		ch, cancel := m.Subscribe(4096)
		defer cancel()
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStop}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Second)
		sawFinished := false
		ready := false
	drain:
		for {
			select {
			case ev := <-ch:
				switch {
				case ev.Type == "tick" && ev.Tick.State == engine.StateFinished:
					sawFinished = true
				case ev.Type == "status" && ev.Status.State == "ready" && sawFinished:
					ready = true
				}
			default:
				break drain
			}
		}
		if !sawFinished || !ready {
			t.Fatalf("finished tick seen=%v, ready status after it=%v", sawFinished, ready)
		}
		if rec.count() != 2 {
			t.Errorf("%d sessions, want 2 (reconnected)", rec.count())
		}
		if l := lastLevel(t, rec.get(0)); l != 1 {
			t.Errorf("first session last level = %d, want 1", l)
		}
		if m.Status().RiderID != testRider.ID {
			t.Errorf("status = %+v", m.Status())
		}
	})
}

func TestDisconnectRefusedWhileRiding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &simRec{}
		m := newMgr(rec, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		ride(t, m)
		if err := m.Disconnect(t.Context()); !errors.Is(err, ErrRiding) {
			t.Fatalf("Disconnect = %v, want ErrRiding", err)
		}
		if err := m.Release(t.Context()); err != nil {
			t.Fatalf("Release = %v", err)
		}
		if st := m.Status(); st.State != "idle" || st.RiderID != 0 {
			t.Errorf("status = %+v", st)
		}
		if l := lastLevel(t, rec.get(0)); l != 1 {
			t.Errorf("last level = %d, want 1", l)
		}
		if err := m.Release(t.Context()); err != nil {
			t.Errorf("second Release = %v", err)
		}
		if err := m.Disconnect(t.Context()); err != nil {
			t.Errorf("Disconnect when idle = %v", err)
		}
	})
}

func TestDisconnectWhenReady(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		if err := m.Disconnect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if m.Status().State != "idle" {
			t.Errorf("status = %+v", m.Status())
		}
	})
}

func TestIdleAutoRelease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, time.Second, func(s *settings.Settings) { s.Bike.IdleReleaseMin = 10 }, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		t0 := time.Now()
		time.Sleep(10*time.Minute - 1*time.Second - time.Since(t0))
		if st := m.Status().State; st != "ready" {
			t.Fatalf("at 9m59s state = %s", st)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		st := m.Status()
		if st.State != "idle" || st.LastError != "released after 10 min idle" {
			t.Fatalf("at 10m status = %+v", st)
		}
	})
}

func TestDoResetsIdleTimer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, time.Second, func(s *settings.Settings) { s.Bike.IdleReleaseMin = 10 }, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		time.Sleep(9 * time.Minute)
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdSetLevel, Value: 6}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		if st := m.Status().State; st != "ready" {
			t.Fatalf("state = %s, want ready (Do reset the timer)", st)
		}
	})
}

func TestNoIdleReleaseWhileRiding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, time.Second, func(s *settings.Settings) { s.Bike.IdleReleaseMin = 10 }, Deps{})
		defer m.Close(context.Background())
		ride(t, m)
		time.Sleep(20 * time.Minute)
		if st := m.Status(); st.State != "riding" || st.LastError != "" {
			t.Fatalf("status = %+v", st)
		}
	})
}

func TestDoWhenIdle(t *testing.T) {
	t.Parallel()
	m := newMgr(nil, 0, nil, Deps{})
	defer m.Close(context.Background())
	if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStart}); !errors.Is(err, ErrNoBike) {
		t.Fatalf("Do = %v, want ErrNoBike", err)
	}
	if tk := m.Snapshot(); tk.State != engine.StateIdle {
		t.Errorf("snapshot = %+v", tk)
	}
}

func TestHintNotFound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
			s.Bike.Driver, s.Bike.Address = "auto", "AA:BB:CC:DD:EE:FF"
		}, Deps{OpenLocal: func() (blue.Transport, error) { return bluetest.NewFake(), nil }})
		defer m.Close(context.Background())
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Fatal(err)
		}
		// bike.BLEDial gives a missing device 30 s before reporting it.
		time.Sleep(31 * time.Second)
		st := m.Status()
		if st.State != "connecting" || !strings.HasPrefix(st.Hint, "Bike not found.") || st.LastError == "" {
			t.Fatalf("status = %+v", st)
		}
		if err := m.Release(t.Context()); err != nil {
			t.Errorf("Release of a never-connected bike = %v, want nil", err)
		}
	})
}

func TestNoAddress(t *testing.T) {
	t.Parallel()
	m := newMgr(nil, 0, func(s *settings.Settings) { s.Bike.Driver = "auto" }, Deps{})
	defer m.Close(context.Background())
	if _, err := m.Connect(t.Context(), testRider); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("Connect = %v, want ErrNoAddress", err)
	}
	if m.Status().State != "idle" {
		t.Errorf("status = %+v", m.Status())
	}
}

func TestNoESPHomeHost(t *testing.T) {
	t.Parallel()
	m := newMgr(nil, 0, func(s *settings.Settings) {
		s.Bike.Driver, s.Bike.Address, s.Bike.Transport = "ftms", "AA:BB:CC:DD:EE:FF", "esphome"
	}, Deps{})
	defer m.Close(context.Background())
	if _, err := m.Connect(t.Context(), testRider); !errors.Is(err, ErrNoHost) {
		t.Fatalf("Connect = %v, want ErrNoHost", err)
	}
}

func TestESPHomeTransportShared(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := fitPlusEcho()
		var opens []string
		m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
			s.Bike.Driver, s.Bike.Address, s.Bike.Transport = "fitplus", testAddr.String(), "esphome"
			s.Bike.ESPHomeHost, s.Bike.ESPHomeKey = "proxy.lan", "k"
		}, Deps{OpenESPHome: func(_ context.Context, hp, key string) (blue.Transport, error) {
			opens = append(opens, hp+"|"+key)
			return fake, nil
		}})
		defer m.Close(context.Background())
		start(t, m)
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStop}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready") // reconnected on a fresh engine
		if len(opens) != 1 || opens[0] != "proxy.lan:6053|k" {
			t.Fatalf("opens = %v, want one open of proxy.lan:6053", opens)
		}
	})
}

func TestFakeFitPlusRide(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := fitPlusEcho()
		m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
			s.Bike.Driver, s.Bike.Address = "auto", testAddr.String()
		}, Deps{OpenLocal: func() (blue.Transport, error) { return fake, nil }})
		defer m.Close(context.Background())
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 10*time.Second, "fitplus ready", func() bool { return m.Status().State == "ready" })
		if st := m.Status(); st.Protocol != "fitplus" || !st.Connected || st.Driver != "auto" {
			t.Fatalf("status = %+v", st)
		}
		if err := m.Release(t.Context()); err != nil {
			t.Fatalf("Release = %v", err)
		}
		w := fake.Writes(testAddr)
		if len(w) == 0 {
			t.Fatal("no writes")
		}
	})
}

func TestSubscribeSurvivesEngineSwap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		ch, cancel := m.Subscribe(8192)
		defer cancel()
		if ev := <-ch; ev.Type != "status" || ev.Status.State != "idle" {
			t.Fatalf("first event = %+v", ev)
		}
		if ev := <-ch; ev.Type != "tick" || ev.Tick.State != engine.StateIdle {
			t.Fatalf("second event = %+v", ev)
		}
		ride(t, m)
		time.Sleep(time.Second)
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStop}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready")
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStart}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		var finishedAt, lastRiding = -1, -1
		for i := 0; ; i++ {
			select {
			case ev := <-ch:
				if ev.Type != "tick" {
					continue
				}
				if ev.Tick.State == engine.StateFinished && finishedAt < 0 {
					finishedAt = i
				}
				if ev.Tick.State == engine.StateRiding {
					lastRiding = i
				}
				continue
			default:
			}
			break
		}
		if finishedAt < 0 || lastRiding < finishedAt {
			t.Fatalf("finished at %d, last riding tick at %d: want riding ticks after the swap", finishedAt, lastRiding)
		}
	})
}

func TestCloseReleases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &simRec{}
		m := newMgr(rec, 20*time.Millisecond, func(s *settings.Settings) { s.Bike.IdleReleaseMin = 10 }, Deps{})
		ride(t, m)
		ch, _ := m.Subscribe(4)
		if err := m.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if m.Status().State != "idle" {
			t.Errorf("status = %+v", m.Status())
		}
		if l := lastLevel(t, rec.get(0)); l != 1 {
			t.Errorf("last level = %d, want 1", l)
		}
		for range ch { // Close ends subscriptions
		}
		if _, err := m.Connect(t.Context(), testRider); !errors.Is(err, ErrClosed) {
			t.Errorf("Connect after Close = %v", err)
		}
		// The bubble ends only when every goroutine has exited.
	})
}

func TestReleaseReportsUnconfirmedFloor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &simRec{}
		m := newMgr(rec, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		ride(t, m)
		rec.get(0).Drop() // link lost; the supervisor is backing off
		time.Sleep(100 * time.Millisecond)
		err := m.Release(t.Context())
		if !errors.Is(err, bike.ErrNoSession) {
			t.Fatalf("Release = %v, want wrapping ErrNoSession", err)
		}
		if st := m.Status(); st.State != "idle" || !strings.Contains(st.LastError, "lower resistance") {
			t.Fatalf("status = %+v", st)
		}
	})
}

func TestScanBusy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		if _, err := m.Scan(t.Context(), ScanRequest{}); !errors.Is(err, ErrBusy) {
			t.Fatalf("Scan = %v, want ErrBusy", err)
		}
	})
}

func TestConnectBusyWhileScanning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := &advTransport{}
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{OpenLocal: func() (blue.Transport, error) { return tr, nil }})
		defer m.Close(context.Background())
		done := make(chan error, 1)
		go func() { _, err := m.Scan(t.Context(), ScanRequest{Duration: 5 * time.Second}); done <- err }()
		time.Sleep(time.Second)
		if _, err := m.Connect(t.Context(), testRider); !errors.Is(err, ErrBusy) {
			t.Errorf("Connect during scan = %v, want ErrBusy", err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !tr.closed.Load() {
			t.Error("scan transport not closed")
		}
		if _, err := m.Connect(t.Context(), testRider); err != nil {
			t.Errorf("Connect after scan = %v", err)
		}
	})
}

func TestHintFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{blue.ErrNotFound, "Bike not found."},
		{errors.Join(errors.New("x"), blue.ErrNoSlots), "The ESPHome proxy has no free"},
		{errors.New("dbus: Access Denied"), "Wattson cannot reach BlueZ"},
		{errors.New("open: permission denied"), "Wattson cannot reach BlueZ"},
		{errors.New("boom"), "Bike busy or unreachable."},
	}
	for _, c := range cases {
		if got := hintFor(c.err); !strings.HasPrefix(got, c.want) {
			t.Errorf("hintFor(%v) = %q, want prefix %q", c.err, got, c.want)
		}
	}
}
