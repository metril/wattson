package ridectl

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/sensors"
	"github.com/metril/wattson/internal/settings"
)

func bpmNow(m *Manager) int { return m.Snapshot().HeartRateBpm }

func TestHRWithSimBike(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := bluetest.NewFake(hrDevice())
		m := newMgr(&simRec{}, 20*time.Millisecond, withHR, Deps{OpenLocal: func() (blue.Transport, error) { return fake, nil }})
		defer m.Close(context.Background())
		events, unsub := m.Subscribe(256)
		defer unsub()
		start(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { hr := m.Status().HR; return hr != nil && hr.Connected })
		fake.Notify(hrAddr, sensors.HRMeasurement, bpmFrame(0x48))
		waitFor(t, 5*time.Second, "tick with 72 bpm", func() bool { return bpmNow(m) == 72 })
		sawConnected := false
		for len(events) > 0 {
			if ev := <-events; ev.Type == "status" && ev.Status.HR != nil && ev.Status.HR.Connected {
				sawConnected = true
			}
		}
		if !sawConnected {
			t.Error("no status event announced the heart rate connection")
		}
	})
}

func TestHROffHasNoStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
		defer m.Close(context.Background())
		start(t, m)
		if hr := m.Status().HR; hr != nil {
			t.Fatalf("HR = %+v, want nil when the source is off", hr)
		}
	})
}

func TestHRDropDoesNotAffectRide(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := fitPlusEcho(hrDevice())
		m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
			s.Bike.Driver, s.Bike.Address = "fitplus", testAddr.String()
			withHR(s)
		}, Deps{OpenLocal: func() (blue.Transport, error) { return fake, nil }})
		defer m.Close(context.Background())
		ride(t, m)
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdSetLevel, Value: 5}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 5*time.Second, "HR connected", func() bool { hr := m.Status().HR; return hr != nil && hr.Connected })
		fake.Notify(hrAddr, sensors.HRMeasurement, bpmFrame(0x48))
		waitFor(t, 5*time.Second, "72 bpm", func() bool { return bpmNow(m) == 72 })
		time.Sleep(3 * time.Second) // let the level settle
		levelWrites := func() (n int) {
			for _, w := range fake.Writes(testAddr) {
				if len(w) == 7 && w[1] == 0x44 && w[2] == 0x05 {
					n++
				}
			}
			return n
		}
		before := levelWrites()

		fake.DropLink(hrAddr)
		time.Sleep(6 * time.Second)
		tk := m.Snapshot()
		if tk.State != engine.StateRiding || tk.HeartRateBpm != 0 || tk.Alert != "" || tk.Level != 5 {
			t.Fatalf("tick after HR drop = %+v", tk)
		}
		if after := levelWrites(); after != before {
			t.Errorf("%d level writes after the HR drop, want none", after-before)
		}
		// The monitor reconnects on its own (1 s backoff, 6 s ago) and feeds the ride again.
		if hr := m.Status().HR; hr == nil || !hr.Connected {
			t.Fatalf("HR status = %+v, want reconnected", hr)
		}
		fake.Notify(hrAddr, sensors.HRMeasurement, bpmFrame(0x50))
		waitFor(t, 5*time.Second, "80 bpm", func() bool { return bpmNow(m) == 80 })
		if st := m.Status().State; st != "riding" {
			t.Fatalf("state = %s", st)
		}
	})
}

func TestHRReconnectsOneSecondAfterDrop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spy := newSpy(bluetest.NewFake(hrDevice()))
		m := newMgr(&simRec{}, 20*time.Millisecond, withHR, Deps{OpenLocal: func() (blue.Transport, error) { return spy, nil }})
		defer m.Close(context.Background())
		start(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { return spy.count(hrAddr) == 1 })
		spy.Transport.(*bluetest.Fake).DropLink(hrAddr)
		time.Sleep(900 * time.Millisecond)
		if n := spy.count(hrAddr); n != 1 {
			t.Fatalf("%d connects 0.9 s after the drop, want 1", n)
		}
		time.Sleep(200 * time.Millisecond)
		if n := spy.count(hrAddr); n != 2 {
			t.Fatalf("%d connects 1.1 s after the drop, want 2", n)
		}
	})
}

func TestHRSurvivesEngineSwap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spy := newSpy(bluetest.NewFake(hrDevice()))
		m := newMgr(&simRec{}, 20*time.Millisecond, withHR, Deps{OpenLocal: func() (blue.Transport, error) { return spy, nil }})
		defer m.Close(context.Background())
		ride(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { hr := m.Status().HR; return hr != nil && hr.Connected })
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdStop}); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "ready") // reconnected on a fresh engine
		spy.Transport.(*bluetest.Fake).Notify(hrAddr, sensors.HRMeasurement, bpmFrame(0x48))
		waitFor(t, 5*time.Second, "72 bpm on the new engine", func() bool { return bpmNow(m) == 72 })
		if n := spy.count(hrAddr); n != 1 {
			t.Fatalf("HR connected %d times across the engine swap, want 1", n)
		}
	})
}

func TestSharedTransportOneOpen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spy := newSpy(fitPlusEcho(hrDevice()))
		opens := 0
		m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
			s.Bike.Driver, s.Bike.Address = "fitplus", testAddr.String()
			withHR(s)
		}, Deps{OpenLocal: func() (blue.Transport, error) { opens++; return spy, nil }})
		defer m.Close(context.Background())
		start(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { hr := m.Status().HR; return hr != nil && hr.Connected })
		if opens != 1 {
			t.Fatalf("OpenLocal called %d times, want 1", opens)
		}
		if spy.count(testAddr) != 1 || spy.count(hrAddr) != 1 {
			t.Fatalf("connects bike=%d hr=%d on the shared fake", spy.count(testAddr), spy.count(hrAddr))
		}
	})
}

func TestReleaseStopsHR(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spy := newSpy(bluetest.NewFake(hrDevice()))
		m := newMgr(&simRec{}, 20*time.Millisecond, withHR, Deps{OpenLocal: func() (blue.Transport, error) { return spy, nil }})
		defer m.Close(context.Background())
		start(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { return spy.count(hrAddr) == 1 })
		if err := m.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
		spy.mu.Lock()
		defer spy.mu.Unlock()
		if spy.closes != 1 || !spy.hrClosedFirst {
			t.Fatalf("transport closes = %d, HR conn closed before it = %v", spy.closes, spy.hrClosedFirst)
		}
		if st := m.Status(); st.State != "idle" || st.HR != nil {
			t.Fatalf("status = %+v", st)
		}
	})
}
