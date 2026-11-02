package ridectl

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

var testRider = store.Rider{ID: 7, UserID: 1, Name: "r", MinLevel: 1, MaxLevel: 16, OffsetLevel: 5, WeightKg: 75, BikeKg: 8, CdA: 0.32, Crr: 0.004}

// simRec records every simulated session the manager dials.
type simRec struct {
	mu   sync.Mutex
	sess []*sim.Session
}

func (r *simRec) dial(c sim.Config) bike.Dial {
	inner := sim.Dial(c)
	return func(ctx context.Context) (bike.Session, error) {
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
func fitPlusEcho() *bluetest.Fake {
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
	f = bluetest.NewFake(dev)
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
