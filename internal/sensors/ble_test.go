package sensors

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

var (
	hrAddr1 = blue.Address{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x01}}
	hrAddr2 = blue.Address{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x02}}
)

func hrDevice(a blue.Address, name string) *bluetest.Device {
	return &bluetest.Device{
		Adv: blue.Advertisement{Addr: a, Name: name, Services: []blue.UUID{HRService}},
		Services: []blue.Service{{UUID: HRService, Chars: []blue.Characteristic{
			{UUID: HRMeasurement, Props: blue.PropNotify},
		}}},
	}
}

// spyTransport records connects and the conns handed out.
type spyTransport struct {
	blue.Transport
	mu       sync.Mutex
	connects []time.Time
	addrs    []blue.Address
	conns    []blue.Conn
}

func (s *spyTransport) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	c, err := s.Transport.Connect(ctx, a)
	s.mu.Lock()
	s.connects = append(s.connects, time.Now())
	s.addrs = append(s.addrs, a)
	if err == nil {
		s.conns = append(s.conns, c)
	}
	s.mu.Unlock()
	return c, err
}

func (s *spyTransport) snapshot() ([]time.Time, []blue.Address, []blue.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.connects...), append([]blue.Address(nil), s.addrs...), append([]blue.Conn(nil), s.conns...)
}

// runMonitor starts m inside the current bubble; stop cancels it and waits.
func runMonitor(t *testing.T, m Monitor) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	return func() error { cancel(); return <-done }
}

func bpm(m Monitor) int { b, _ := m.Latest(); return b }

func TestBLEHRSamples(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		m := NewBLE(BLEConfig{Transport: f, Addr: hrAddr1})
		if b, at := m.Latest(); b != 0 || !at.IsZero() {
			t.Fatalf("Latest before reading = %d, %v", b, at)
		}
		stop := runMonitor(t, m)
		synctest.Wait()
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x48})
		b, at := m.Latest()
		if b != 72 || !at.Equal(time.Now()) {
			t.Fatalf("Latest = %d at %v", b, at)
		}
		want := HRStatus{Source: "ble", Target: "AA:BB:CC:DD:EE:01", Connected: true, BPM: 72, LastAt: at}
		if got := m.Status(); got != want {
			t.Fatalf("Status = %+v, want %+v", got, want)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
		f.Close()
	})
}

func TestBLEHRZeroIgnored(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		m := NewBLE(BLEConfig{Transport: f, Addr: hrAddr1})
		stop := runMonitor(t, m)
		synctest.Wait()
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x48})
		time.Sleep(time.Second)
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x00})
		f.Notify(hrAddr1, HRMeasurement, []byte{0x01}) // malformed
		f.Notify(hrAddr1, HRMeasurement, nil)
		b, at := m.Latest()
		if b != 72 || !at.Equal(time.Now().Add(-time.Second)) {
			t.Fatalf("Latest = %d at %v", b, at)
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRIgnoresReadingWithoutContact(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		m := NewBLE(BLEConfig{Transport: f, Addr: hrAddr1})
		stop := runMonitor(t, m)
		synctest.Wait()
		f.Notify(hrAddr1, HRMeasurement, []byte{0x06, 0x48})
		f.Notify(hrAddr1, HRMeasurement, []byte{0x04, 0x99}) // contact supported, not detected
		if b := bpm(m); b != 72 {
			t.Fatalf("Latest = %d, want 72", b)
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRReconnects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1})
		stop := runMonitor(t, m)
		synctest.Wait()
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x48})
		f.DropLink(hrAddr1)
		synctest.Wait()
		if m.Status().Connected {
			t.Fatal("still Connected after DropLink")
		}
		if st := m.Status(); st.Error == "" || st.BPM != 72 {
			t.Fatalf("Status after drop = %+v", st)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		cs, _, _ := spy.snapshot()
		if len(cs) != 2 || cs[1].Sub(cs[0]) != time.Second {
			t.Fatalf("connects = %v", cs)
		}
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x50})
		if b := bpm(m); b != 80 {
			t.Fatalf("Latest = %d, want 80", b)
		}
		if st := m.Status(); !st.Connected || st.Error != "" {
			t.Fatalf("Status = %+v", st)
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRBackoffResetsAfterReading(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1})
		stop := runMonitor(t, m)
		synctest.Wait()
		// Connection 1 drops without data, connection 2 after a reading, then
		// connection 3 must follow after the minimum backoff again.
		f.DropLink(hrAddr1) // no reading: next backoff is 1s, then doubles to 2s
		time.Sleep(time.Second)
		synctest.Wait()
		f.Notify(hrAddr1, HRMeasurement, []byte{0x00, 0x48})
		f.DropLink(hrAddr1) // reading delivered: backoff resets
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		cs, _, _ := spy.snapshot()
		if len(cs) != 3 {
			t.Fatalf("connects = %d, want 3 (backoff did not reset)", len(cs))
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRNoService(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dev := hrDevice(hrAddr1, "")
		dev.Services = []blue.Service{{UUID: blue.UUID16(0x180a)}}
		f := bluetest.NewFake(dev)
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1})
		stop := runMonitor(t, m)
		time.Sleep(7*time.Second + time.Millisecond)
		synctest.Wait()
		cs, _, conns := spy.snapshot()
		if len(cs) != 4 {
			t.Fatalf("connects = %d, want 4", len(cs))
		}
		for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
			if d := cs[i+1].Sub(cs[i]); d != want {
				t.Fatalf("retry %d after %v, want %v", i+1, d, want)
			}
		}
		for i, c := range conns {
			select {
			case <-c.Done():
			default:
				t.Fatalf("conn %d not closed", i)
			}
		}
		st := m.Status()
		if !strings.Contains(st.Error, "no heart rate service") || st.Connected {
			t.Fatalf("Status = %+v", st)
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRFollowsName(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr2, "HeartCast"))
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1, Name: "HeartCast"})
		stop := runMonitor(t, m)
		synctest.Wait()
		_, addrs, _ := spy.snapshot()
		if len(addrs) != 1 || addrs[0] != hrAddr2 {
			t.Fatalf("connected to %v, want only %v", addrs, hrAddr2)
		}
		f.Notify(hrAddr2, HRMeasurement, []byte{0x00, 0x48})
		if st := m.Status(); st.Target != "AA:BB:CC:DD:EE:02 (HeartCast)" || !st.Connected || st.BPM != 72 {
			t.Fatalf("Status = %+v", st)
		}
		_ = stop()
		f.Close()
	})
}

// A name match needs the heart rate service: a same-named device without it is skipped.
func TestBLEHRNameNeedsService(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		other := hrDevice(hrAddr2, "HeartCast")
		other.Adv.Services = nil
		f := bluetest.NewFake(other)
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1, Name: "HeartCast", ResolveScan: time.Second})
		stop := runMonitor(t, m)
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		_, addrs, _ := spy.snapshot()
		for _, a := range addrs {
			if a == hrAddr2 {
				t.Fatal("connected to a same-named device without the heart rate service")
			}
		}
		if st := m.Status(); st.Connected || st.Error == "" {
			t.Fatalf("Status = %+v", st)
		}
		_ = stop()
		f.Close()
	})
}

func TestBLEHRCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake(hrDevice(hrAddr1, ""))
		spy := &spyTransport{Transport: f}
		m := NewBLE(BLEConfig{Transport: spy, Addr: hrAddr1})
		stop := runMonitor(t, m)
		synctest.Wait()
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		_, _, conns := spy.snapshot()
		if len(conns) != 1 {
			t.Fatalf("conns = %d", len(conns))
		}
		select {
		case <-conns[0].Done():
		default:
			t.Fatal("conn not closed on cancel")
		}
		if m.Status().Connected {
			t.Fatal("Connected after Run returned")
		}
		f.Close()
	})
}
