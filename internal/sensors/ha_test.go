package sensors

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/hass/hasstest"
)

const haTok = "tok"

func haMonitor(s *hasstest.Server, token string) *HAMonitor {
	m, _ := haMonitorCount(s, token)
	return m
}

// haMonitorCount also returns the number of Dial attempts made so far.
func haMonitorCount(s *hasstest.Server, token string) (*HAMonitor, *atomic.Int32) {
	var dials atomic.Int32
	return NewHA(HAConfig{
		Dial: func(ctx context.Context) (*hass.Client, error) {
			dials.Add(1)
			return hass.Dial(ctx, s.URL(), token, hass.Options{PingInterval: time.Second})
		},
		Entity:     "sensor.hr",
		BackoffMin: 20 * time.Millisecond,
		BackoffMax: 40 * time.Millisecond,
	}), &dials
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHAHRInitialAndUpdates(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, haTok)
	s.SetState("sensor.hr", "64", nil)
	m := haMonitor(s, haTok)
	stop := runMonitor(t, m)
	waitFor(t, "initial 64", func() bool { return bpm(m) == 64 })
	s.SetState("sensor.hr", "71.6", nil)
	waitFor(t, "update 72", func() bool { return bpm(m) == 72 })
	st := m.Status()
	if !st.Connected || st.Source != "ha" || st.Target != "sensor.hr" || st.BPM != 72 || st.Error != "" || st.LastAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if m.Status().Connected {
		t.Fatal("still connected after stop")
	}
}

func TestHAHRUnavailableKeepsLast(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, haTok)
	s.SetState("sensor.hr", "64", nil)
	m := haMonitor(s, haTok)
	stop := runMonitor(t, m)
	defer stop()
	waitFor(t, "64", func() bool { return bpm(m) == 64 })
	_, at1 := m.Latest()
	for _, v := range []string{"unavailable", "unknown", "abc", "0", "-5", "NaN"} {
		s.SetState("sensor.hr", v, nil)
	}
	// a valid sentinel proves the invalid states before it were all processed
	s.SetState("sensor.hr", "65", nil)
	waitFor(t, "65", func() bool { return bpm(m) == 65 })
	s.SetState("sensor.hr", "unavailable", nil)
	time.Sleep(30 * time.Millisecond)
	b2, at2 := m.Latest()
	s.SetState("sensor.hr", "unknown", nil)
	time.Sleep(30 * time.Millisecond)
	if b, at := m.Latest(); b != b2 || !at.Equal(at2) || b != 65 {
		t.Fatalf("Latest = %d at %v; want 65 at %v (first 64 at %v)", b, at, at2, at1)
	}
}

func TestHAHRReconnects(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, haTok)
	s.SetState("sensor.hr", "64", nil)
	m := haMonitor(s, haTok)
	stop := runMonitor(t, m)
	defer stop()
	waitFor(t, "64", func() bool { return bpm(m) == 64 })
	s.Drop()
	waitFor(t, "second auth", func() bool { return s.Auths() == 2 })
	waitFor(t, "connected again", func() bool { return m.Status().Connected })
	s.SetState("sensor.hr", "90", nil)
	waitFor(t, "90", func() bool { return bpm(m) == 90 })
}

func TestHAHRBadToken(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, haTok)
	s.SetState("sensor.hr", "64", nil)
	m, dials := haMonitorCount(s, "wrong")
	stop := runMonitor(t, m)
	defer stop()
	waitFor(t, "token error", func() bool { return strings.Contains(m.Status().Error, "token") })
	waitFor(t, "retries", func() bool { return dials.Load() >= 3 })
	if st := m.Status(); st.Connected || st.BPM != 0 || !strings.Contains(st.Error, "token") || strings.Contains(st.Error, "wrong") {
		t.Fatalf("status %+v", st)
	}
	if s.Auths() != 0 {
		t.Fatalf("auths %d", s.Auths())
	}
}

func TestHAHRMissingEntity(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, haTok)
	m := haMonitor(s, haTok)
	stop := runMonitor(t, m)
	defer stop()
	waitFor(t, "not found", func() bool { return m.Status().Error == "entity sensor.hr not found" })
	// it retries, and picks the entity up once it exists
	waitFor(t, "second attempt", func() bool { return s.Auths() >= 2 })
	s.SetState("sensor.hr", "80", nil)
	waitFor(t, "80", func() bool { return bpm(m) == 80 })
	waitFor(t, "healthy", func() bool { st := m.Status(); return st.Connected && st.Error == "" })
}

func TestParseBPM(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"64": 64, "71.6": 72, "71.4": 71, "1e2": 100, "0.4": 0, "0": 0, "-3": 0, "unavailable": 0, "unknown": 0, "": 0, "NaN": 0, "Inf": 0, "70000": 0} {
		if got, ok := parseBPM(in); got != want || ok != (want > 0) {
			t.Errorf("parseBPM(%q) = %d, %v", in, got, ok)
		}
	}
}
