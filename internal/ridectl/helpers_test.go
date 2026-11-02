package ridectl

import (
	"context"
	"encoding/binary"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/sensors"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

var testRider = store.Rider{ID: 7, UserID: 1, Name: "r", MinLevel: 1, MaxLevel: 16, OffsetLevel: 5, WeightKg: 75, BikeKg: 8, CdA: 0.32, Crr: 0.004}

// simRec records every simulated session the manager dials.
type simRec struct {
	mu   sync.Mutex
	sess []*sim.Session
	fail atomic.Bool // dials fail while set: the bike is unreachable
}

func (r *simRec) dial(c sim.Config) bike.Dial {
	inner := sim.Dial(c)
	return func(ctx context.Context) (bike.Session, error) {
		if r.fail.Load() {
			return nil, blue.ErrNotFound
		}
		s, err := inner(ctx)
		if err == nil {
			r.mu.Lock()
			r.sess = append(r.sess, s.(*sim.Session))
			r.mu.Unlock()
		}
		return s, err
	}
}

func (r *simRec) get(i int) *sim.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.sess) {
		return nil
	}
	return r.sess[i]
}

func (r *simRec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sess)
}

func lastLevel(t *testing.T, s *sim.Session) int {
	t.Helper()
	l := s.Levels()
	if len(l) == 0 {
		t.Fatal("session saw no levels")
	}
	return l[len(l)-1]
}

// newMgr builds a manager over the simulator (cadence 80) with settings tweaked by mod.
func newMgr(rec *simRec, tick time.Duration, mod func(*settings.Settings), d Deps) *Manager {
	s := settings.Defaults()
	s.Bike.SimCadenceRpm = 80
	if mod != nil {
		mod(&s)
	}
	d.Settings = func(context.Context) (settings.Settings, error) { return s, nil }
	if rec != nil {
		d.SimDial = rec.dial
	}
	d.TickEvery, d.SimPeriod = tick, tick
	return New(d)
}

// waitFor polls (in virtual time under synctest) until cond holds.
func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitState(t *testing.T, m *Manager, state string) {
	t.Helper()
	waitFor(t, 5*time.Second, "state "+state+" (now "+m.Status().State+")", func() bool { return m.Status().State == state })
}

var testAddr = blue.Address{MAC: [6]byte{0xC0, 0xFF, 0xEE, 0x00, 0x00, 0x01}, Type: blue.AddrRandom}

// fitPlusEcho is a FitPlus bike whose notifications echo the last level written.
func fitPlusEcho(extra ...*bluetest.Device) *bluetest.Fake {
	var f *bluetest.Fake
	var level atomic.Int32
	dev := &bluetest.Device{
		Adv: blue.Advertisement{Addr: testAddr, Name: "MRK-S29R2", RSSI: -50, Services: []blue.UUID{bike.FitPlusService}},
		Services: []blue.Service{
			{UUID: blue.UUID16(0x1800), Chars: []blue.Characteristic{{UUID: blue.UUID16(0x2a00), Props: blue.PropRead}}},
			{UUID: bike.FitPlusService, Chars: []blue.Characteristic{
				{UUID: bike.FitPlusNotify, Props: blue.PropNotify},
				{UUID: bike.FitPlusWrite, Props: blue.PropWrite},
			}},
		},
		Values: map[blue.UUID][]byte{blue.UUID16(0x2a00): []byte("MRK-S29R2")},
	}
	dev.OnWrite = func(ch blue.UUID, data []byte) {
		if ch != bike.FitPlusWrite {
			return
		}
		if len(data) > 0 && data[0] == 0xaa {
			f.Notify(testAddr, bike.FitPlusNotify, make([]byte, 15))
			return
		}
		if len(data) == 7 && data[1] == 0x44 && data[2] == 0x05 {
			level.Store(int32(data[3]))
		}
		if l := level.Load(); l > 0 {
			n := make([]byte, 15)
			n[5], n[6] = byte(l), 60
			binary.LittleEndian.PutUint16(n[9:], 800)
			f.Notify(testAddr, bike.FitPlusNotify, n)
		}
	}
	f = bluetest.NewFake(append([]*bluetest.Device{dev}, extra...)...)
	return f
}

// advTransport is a scan-only transport replaying fixed advertisements.
type advTransport struct {
	advs   []blue.Advertisement
	closed atomic.Bool
}

func (a *advTransport) Scan(ctx context.Context, fn func(blue.Advertisement)) error {
	for _, v := range a.advs {
		fn(v)
	}
	<-ctx.Done()
	return nil
}

func (a *advTransport) Connect(context.Context, blue.Address) (blue.Conn, error) {
	return nil, blue.ErrNotFound
}

func (a *advTransport) Close() error { a.closed.Store(true); return nil }

var hrAddr = blue.Address{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x01}}

// hrDevice is a heart rate monitor at hrAddr.
func hrDevice() *bluetest.Device {
	return &bluetest.Device{
		Adv: blue.Advertisement{Addr: hrAddr, Name: "HRM", RSSI: -55, Services: []blue.UUID{sensors.HRService}},
		Services: []blue.Service{{UUID: sensors.HRService, Chars: []blue.Characteristic{
			{UUID: sensors.HRMeasurement, Props: blue.PropNotify},
		}}},
	}
}

// logBuf is a goroutine-safe log sink.
type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// bpmFrame is a heart rate measurement notification for v bpm (8-bit format).
func bpmFrame(v byte) []byte { return []byte{0x00, v} }

// withHR enables a BLE heart rate source at hrAddr.
func withHR(s *settings.Settings) {
	s.HeartRate = settings.HeartRate{Source: "ble", Address: hrAddr.String()}
}

// spyTransport wraps a transport, recording connects, conns and closes.
type spyTransport struct {
	blue.Transport
	mu       sync.Mutex
	connects map[blue.Address]int
	conns    map[blue.Address]*spyConn
	closes   int
	// hrClosedFirst: the heart rate conn was already closed when the transport closed.
	hrClosedFirst bool
}

func newSpy(t blue.Transport) *spyTransport {
	return &spyTransport{Transport: t, connects: map[blue.Address]int{}, conns: map[blue.Address]*spyConn{}}
}

type spyConn struct {
	blue.Conn
	closed atomic.Bool
}

func (c *spyConn) Close() error { c.closed.Store(true); return c.Conn.Close() }

func (s *spyTransport) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	c, err := s.Transport.Connect(ctx, a)
	if err != nil {
		return nil, err
	}
	sc := &spyConn{Conn: c}
	s.mu.Lock()
	s.connects[a]++
	s.conns[a] = sc
	s.mu.Unlock()
	return sc, nil
}

func (s *spyTransport) Close() error {
	s.mu.Lock()
	s.closes++
	if c := s.conns[hrAddr]; c != nil {
		s.hrClosedFirst = c.closed.Load()
	}
	s.mu.Unlock()
	return s.Transport.Close()
}

func (s *spyTransport) count(a blue.Address) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects[a]
}
