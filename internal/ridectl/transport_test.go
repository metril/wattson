package ridectl

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
	"github.com/metril/wattson/internal/settings"
)

// fakeProxy is a bluetest.Fake that also answers as an ESPHome proxy.
type fakeProxy struct {
	*bluetest.Fake
	done       chan struct{}
	subscribed atomic.Bool
	connectErr error
	statusErr  error
	closes     atomic.Int32
}

func newFakeProxy() *fakeProxy {
	p := &fakeProxy{Fake: bluetest.NewFake(), done: make(chan struct{})}
	p.subscribed.Store(true)
	return p
}

func (p *fakeProxy) Done() <-chan struct{} { return p.done }
func (p *fakeProxy) ProxyStatus(context.Context) (blue.ProxyStatus, error) {
	return blue.ProxyStatus{Subscribed: p.subscribed.Load()}, p.statusErr
}
func (p *fakeProxy) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	if p.connectErr != nil {
		return nil, p.connectErr
	}
	return p.Fake.Connect(ctx, a)
}
func (p *fakeProxy) Close() error { p.closes.Add(1); return p.Fake.Close() }

func TestSharedOpensLazilyAndOnce(t *testing.T) {
	t.Parallel()
	var opens atomic.Int32
	s := newShared(func(context.Context) (blue.Transport, error) { opens.Add(1); return bluetest.NewFake(), nil })
	if opens.Load() != 0 {
		t.Fatal("opened before first use")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Scan(t.Context(), func(blue.Advertisement) {})
		}()
		// A scan blocks until its ctx ends; end it via Close below.
	}
	time.Sleep(20 * time.Millisecond)
	if n := opens.Load(); n != 1 {
		t.Fatalf("opens = %d, want 1", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if _, err := s.Connect(t.Context(), hrAddr); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("Connect after Close = %v", err)
	}
}

func TestSharedOpenErrorIsRetried(t *testing.T) {
	t.Parallel()
	var opens atomic.Int32
	s := newShared(func(context.Context) (blue.Transport, error) {
		if opens.Add(1) == 1 {
			return nil, errors.New("adapter off")
		}
		return bluetest.NewFake(hrDevice()), nil
	})
	defer s.Close()
	if _, err := s.Connect(t.Context(), hrAddr); err == nil || err.Error() != "adapter off" {
		t.Fatalf("first Connect = %v", err)
	}
	c, err := s.Connect(t.Context(), hrAddr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestSharedConnectReportsHeldProxy(t *testing.T) {
	t.Parallel()
	generic := errors.New("connect timed out")
	for _, tc := range []struct {
		name       string
		connectErr error
		subscribed bool
		statusErr  error
		wantHeld   bool
	}{
		{"silent proxy", generic, false, nil, true},
		{"healthy proxy", generic, true, nil, false},
		{"no slots", blue.ErrNoSlots, false, nil, false},
		{"not found", blue.ErrNotFound, false, nil, false},
		{"status fails", generic, false, errors.New("boom"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newFakeProxy()
			p.connectErr, p.statusErr = tc.connectErr, tc.statusErr
			p.subscribed.Store(tc.subscribed)
			s := newShared(func(context.Context) (blue.Transport, error) { return p, nil })
			defer s.Close()
			_, err := s.Connect(t.Context(), hrAddr)
			if got := errors.Is(err, blue.ErrProxyHeld); got != tc.wantHeld {
				t.Fatalf("ErrProxyHeld = %v for %v, want %v", got, err, tc.wantHeld)
			}
			if tc.wantHeld && !strings.Contains(err.Error(), generic.Error()) {
				t.Errorf("held error %q lost the cause", err)
			}
		})
	}
}

// A held proxy is only reported: the shared transport never reopens it, which
// would re-subscribe and steal the proxy back from Home Assistant.
func TestSharedDoesNotReopenHeldProxy(t *testing.T) {
	t.Parallel()
	var opens atomic.Int32
	p := newFakeProxy()
	p.connectErr = errors.New("timeout")
	p.subscribed.Store(false)
	s := newShared(func(context.Context) (blue.Transport, error) { opens.Add(1); return p, nil })
	defer s.Close()
	for range 3 {
		if _, err := s.Connect(t.Context(), hrAddr); !errors.Is(err, blue.ErrProxyHeld) {
			t.Fatalf("Connect = %v", err)
		}
	}
	if n := opens.Load(); n != 1 {
		t.Fatalf("opens = %d, want 1", n)
	}
}

func TestSharedReopensAfterProxyLinkLoss(t *testing.T) {
	t.Parallel()
	var opens atomic.Int32
	var mu sync.Mutex
	var made []*fakeProxy
	s := newShared(func(context.Context) (blue.Transport, error) {
		opens.Add(1)
		p := newFakeProxy()
		p.Fake = bluetest.NewFake(hrDevice())
		mu.Lock()
		made = append(made, p)
		mu.Unlock()
		return p, nil
	})
	defer s.Close()
	c, err := s.Connect(t.Context(), hrAddr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	mu.Lock()
	first := made[0]
	mu.Unlock()
	close(first.done) // the proxy link dropped
	var wg sync.WaitGroup
	for range 4 { // concurrent users reopen exactly once
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := s.Connect(t.Context(), hrAddr)
			if err != nil {
				t.Error(err)
				return
			}
			_ = c.Close()
		}()
	}
	wg.Wait()
	if n := opens.Load(); n != 2 {
		t.Fatalf("opens = %d, want 2", n)
	}
	deadline := time.Now().Add(2 * time.Second)
	for first.closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if first.closes.Load() != 1 {
		t.Errorf("dead transport closed %d times, want 1", first.closes.Load())
	}
}

// --- through the manager, over a real ESPHome API socket ---

const hrNotifyHandle = 3

func advData(name string, svc uint16) []byte {
	d := append([]byte{byte(len(name) + 1), 0x09}, name...)
	return append(d, 3, 0x03, byte(svc), byte(svc>>8))
}

// proxyServer is an ESPHome proxy with a FitPlus bike (testAddr) and a heart rate monitor (hrAddr).
func proxyServer(t *testing.T) (*esphometest.Server, string) {
	t.Helper()
	k := bytes.Repeat([]byte{9}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	srv.AddBLEDevice(esphometest.RawAdv{Address: testAddr.Uint64(), RSSI: -60, AddressType: 1, Data: advData("MRK-S29R2", 0xfff0)},
		[]esphometest.GATTService{{UUID: "fff0", Handle: 1, Chars: []esphometest.GATTChar{
			{UUID: "fff1", Handle: 3, Props: uint32(blue.PropNotify)},
			{UUID: "fff2", Handle: 6, Props: uint32(blue.PropWrite | blue.PropWriteNoResp)},
		}}})
	srv.AddBLEDevice(esphometest.RawAdv{Address: hrAddr.Uint64(), RSSI: -55, Data: advData("HRM", 0x180d)},
		[]esphometest.GATTService{{UUID: "180d", Handle: 1, Chars: []esphometest.GATTChar{
			{UUID: "2a37", Handle: hrNotifyHandle, Props: uint32(blue.PropNotify)},
		}}})
	return srv, base64.StdEncoding.EncodeToString(k)
}

func espSettings(srv *esphometest.Server, key string, hr bool) func(*settings.Settings) {
	return func(s *settings.Settings) {
		s.Bike.Driver, s.Bike.Address, s.Bike.Transport = "fitplus", testAddr.String(), "esphome"
		s.Bike.ESPHomeHost, s.Bike.ESPHomeKey = srv.Addr(), key
		if hr {
			withHR(s)
		}
	}
}

func poll(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(limit); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// levelWrites are the level values written to the FitPlus bike, in order.
func levelWrites(srv *esphometest.Server) []byte {
	var out []byte
	for _, w := range srv.BLEWrites(testAddr.Uint64()) {
		if len(w) == 7 && w[1] == 0x44 && w[2] == 0x05 {
			out = append(out, w[3])
		}
	}
	return out
}

// Losing the proxy link reopens the shared transport once, for the bike and the
// heart rate monitor together, and the ride picks up at its target level.
func TestProxyLinkLossReopensOnce(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	var opens atomic.Int32
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, true), Deps{
		HRBackoffMin: 20 * time.Millisecond,
		OpenESPHome: func(ctx context.Context, hp, k string) (blue.Transport, error) {
			opens.Add(1)
			return blue.NewESPHome(ctx, hp, k)
		},
	})
	defer m.Close(context.Background())
	ride(t, m)
	if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdSetLevel, Value: 7}); err != nil {
		t.Fatal(err)
	}
	poll(t, 2*time.Second, "level 7 written", func() bool { l := levelWrites(srv); return len(l) > 0 && l[len(l)-1] == 7 })
	poll(t, 2*time.Second, "HR connected", func() bool { hr := m.Status().HR; return hr != nil && hr.Connected })
	n := len(levelWrites(srv))

	srv.CloseClient()
	// The supervisor waits 1 s before redialling a lost session.
	poll(t, 5*time.Second, "level 7 re-asserted", func() bool { return len(levelWrites(srv)) > n })
	if got := levelWrites(srv)[n]; got != 7 {
		t.Fatalf("first write after the reopen set level %d, want 7", got)
	}
	poll(t, 2*time.Second, "HR connected again", func() bool {
		hr := m.Status().HR
		return hr != nil && hr.Connected
	})
	srv.NotifyBLE(hrAddr.Uint64(), hrNotifyHandle, bpmFrame(0x50))
	poll(t, 2*time.Second, "80 bpm", func() bool { return bpmNow(m) == 80 })
	if got := opens.Load(); got != 2 {
		t.Fatalf("OpenESPHome called %d times, want 2", got)
	}
	if st := m.Status().State; st != "riding" {
		t.Fatalf("state = %s", st)
	}
}

// A proxy whose Bluetooth another client took is reported with a hint and never
// rebuilt: reconnecting would take it back from Home Assistant.
func TestHintProxyHeld(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	var opens atomic.Int32
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{
		OpenESPHome: func(ctx context.Context, hp, k string) (blue.Transport, error) {
			opens.Add(1)
			tr, err := blue.NewESPHomeWith(ctx, hp, k, blue.WithHeldAfter(20*time.Millisecond))
			if err != nil {
				return nil, err
			}
			p := tr.(blue.Proxy)
			if _, err := p.ProxyStatus(ctx); err != nil { // the proxy has processed our subscription
				return nil, err
			}
			srv.SetForeignSubscriber()
			time.Sleep(40 * time.Millisecond) // adverts have been silent past heldAfter
			return tr, nil
		},
	})
	defer m.Close(context.Background())
	if _, err := m.Connect(t.Context(), testRider); err != nil {
		t.Fatal(err)
	}
	poll(t, 2*time.Second, "proxy-held hint", func() bool { return strings.HasPrefix(m.Status().Hint, "Another client") })
	if st := m.Status(); st.State != "connecting" || !strings.Contains(st.LastError, blue.ErrProxyHeld.Error()) {
		t.Fatalf("status = %+v", st)
	}
	if f := srv.BLEAdvFlags(); len(f) != 1 {
		t.Fatalf("advert subscriptions = %v: Wattson must not take the proxy back", f)
	}
	if n := opens.Load(); n != 1 {
		t.Fatalf("OpenESPHome called %d times, want 1", n)
	}
}
