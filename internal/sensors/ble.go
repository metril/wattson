package sensors

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metril/wattson/internal/blue"
)

// BLEConfig configures a BLE heart rate monitor. Zero durations select the defaults.
type BLEConfig struct {
	Transport blue.Transport
	Addr      blue.Address
	// Name is the monitor's advertised name. Some monitors (HeartCast) rotate
	// their address, so when set each attempt scans for Addr or for a device
	// with this name that advertises the heart rate service. "" skips the scan.
	Name string

	ResolveScan    time.Duration // longest name scan per attempt; default 10s
	ConnectTimeout time.Duration // bound on Connect and Subscribe; default 30s
	BackoffMin     time.Duration // default 1s
	BackoffMax     time.Duration // default 30s
	// ProxyCheck is how often a live connection through a blue.Proxy checks
	// that the proxy still serves us (its advertisement stream has not gone
	// silent); default 15s. Two consecutive failed checks mean another client
	// took the proxy's Bluetooth subscription: the connection ends with
	// blue.ErrProxyHeld. The monitor only reports this and retries with
	// backoff; it never takes the subscription back.
	ProxyCheck time.Duration
}

// BLEMonitor reads heart rate from a BLE heart rate monitor, reconnecting with
// backoff forever. Its failures are reported through Status only.
type BLEMonitor struct {
	c BLEConfig

	last atomic.Pointer[hrSample]

	sawContact  atomic.Bool // a reading with sensor contact detected arrived
	contactWarn sync.Once

	mu        sync.Mutex
	target    string
	connected bool
	errText   string
}

type hrSample struct {
	bpm int
	at  time.Time
}

var _ Monitor = (*BLEMonitor)(nil)

// NewBLE returns a monitor for c; call Run to start it.
func NewBLE(c BLEConfig) *BLEMonitor {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&c.ResolveScan, 10*time.Second)
	def(&c.ConnectTimeout, 30*time.Second)
	def(&c.BackoffMin, time.Second)
	def(&c.BackoffMax, 30*time.Second)
	def(&c.ProxyCheck, 15*time.Second)
	m := &BLEMonitor{c: c}
	m.target = m.targetString(c.Addr)
	return m
}

func (m *BLEMonitor) targetString(a blue.Address) string {
	if m.c.Name != "" {
		return fmt.Sprintf("%s (%s)", a, m.c.Name)
	}
	return a.String()
}

func (m *BLEMonitor) Latest() (int, time.Time) {
	if s := m.last.Load(); s != nil {
		return s.bpm, s.at
	}
	return 0, time.Time{}
}

func (m *BLEMonitor) Status() HRStatus {
	bpm, at := m.Latest()
	m.mu.Lock()
	defer m.mu.Unlock()
	return HRStatus{Source: "ble", Target: m.target, Connected: m.connected, BPM: bpm, LastAt: at, Error: m.errText}
}

func (m *BLEMonitor) set(f func()) {
	m.mu.Lock()
	f()
	m.mu.Unlock()
}

// Run connects and reconnects until ctx is done.
func (m *BLEMonitor) Run(ctx context.Context) error {
	defer m.set(func() { m.connected = false })
	return backoffLoop(ctx, func(ctx context.Context) (bool, error) {
		got, err := m.attempt(ctx)
		if ctx.Err() == nil {
			m.set(func() {
				m.connected = false
				if err != nil {
					m.errText = err.Error()
				}
			})
		}
		return got, err
	}, m.c.BackoffMin, m.c.BackoffMax)
}

// resolve returns the address to connect to, scanning for the name first when one is set.
func (m *BLEMonitor) resolve(ctx context.Context) (blue.Address, error) {
	if m.c.Name == "" {
		return m.c.Addr, nil
	}
	sctx, cancel := context.WithTimeout(ctx, m.c.ResolveScan)
	defer cancel()
	var (
		mu    sync.Mutex
		found bool
		addr  blue.Address
	)
	err := m.c.Transport.Scan(sctx, func(a blue.Advertisement) {
		mu.Lock()
		defer mu.Unlock()
		if found {
			return
		}
		if a.Addr == m.c.Addr || (a.Name == m.c.Name && slices.Contains(a.Services, HRService)) {
			found, addr = true, a.Addr
			cancel()
		}
	})
	mu.Lock()
	defer mu.Unlock()
	if found {
		return addr, nil
	}
	if ctx.Err() != nil {
		return blue.Address{}, ctx.Err()
	}
	// A proxy held by another client delivers no Bluetooth data at all.
	if herr := m.proxyHeld(ctx); herr != nil {
		return blue.Address{}, herr
	}
	if err != nil {
		return blue.Address{}, fmt.Errorf("scan for %s: %w", m.targetString(m.c.Addr), err)
	}
	return blue.Address{}, fmt.Errorf("heart rate monitor %s not seen in %s: %w", m.targetString(m.c.Addr), m.c.ResolveScan, blue.ErrNotFound)
}

// proxyHeld returns blue.ErrProxyHeld when the transport is a proxy that does
// serve us (advertisements went silent); nil otherwise, including when the transport is no proxy.
func (m *BLEMonitor) proxyHeld(ctx context.Context) error {
	p, ok := m.c.Transport.(blue.Proxy)
	if !ok {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, min(proxyProbe, m.c.ProxyCheck))
	defer cancel()
	st, err := p.ProxyStatus(pctx)
	if err != nil || st.Subscribed || ctx.Err() != nil {
		return nil
	}
	return blue.ProxyHeldError("heart rate monitor "+m.targetString(m.c.Addr), st.Name)
}

// proxyProbe bounds one proxy liveness query.
const proxyProbe = 3 * time.Second

func hasHR(svcs []blue.Service) bool {
	for _, s := range svcs {
		if s.UUID != HRService {
			continue
		}
		for _, ch := range s.Chars {
			if ch.UUID == HRMeasurement {
				return true
			}
		}
	}
	return false
}

// attempt makes one connection and holds it until it drops. got reports whether
// at least one reading arrived.
func (m *BLEMonitor) attempt(ctx context.Context) (got bool, err error) {
	addr, err := m.resolve(ctx)
	if err != nil {
		return false, err
	}
	m.set(func() { m.target = m.targetString(addr) })

	cctx, cancel := context.WithTimeout(ctx, m.c.ConnectTimeout)
	conn, err := m.c.Transport.Connect(cctx, addr)
	cancel()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if !hasHR(conn.Services()) {
		return false, ErrNoHRService
	}
	var gotAny atomic.Bool
	sctx, cancel := context.WithTimeout(ctx, m.c.ConnectTimeout)
	err = conn.Subscribe(sctx, HRMeasurement, func(b []byte) {
		h, err := ParseHRM(b)
		if err != nil || h.BPM == 0 {
			return
		}
		if h.Contact {
			m.sawContact.Store(true)
		}
		if h.ContactSupported && !h.Contact {
			if !m.sawContact.Load() {
				m.contactWarn.Do(func() {
					slog.Warn("heart rate monitor reports sensor contact supported but never detected; ignoring its readings", "target", m.Status().Target)
				})
			}
			return
		}
		m.last.Store(&hrSample{bpm: h.BPM, at: time.Now()})
		gotAny.Store(true)
	})
	cancel()
	if err != nil {
		return false, err
	}
	m.set(func() { m.connected, m.errText = true, "" })
	var check <-chan time.Time
	if _, ok := m.c.Transport.(blue.Proxy); ok {
		tk := time.NewTicker(m.c.ProxyCheck)
		defer tk.Stop()
		check = tk.C
	}
	held := 0 // consecutive held proxy checks
wait:
	for {
		select {
		case <-ctx.Done():
			return gotAny.Load(), nil
		case <-conn.Done():
			break wait
		case <-check:
			herr := m.proxyHeld(ctx)
			if herr == nil {
				held = 0
				continue
			}
			if held++; held >= 2 {
				// Report now: closing the conn waits on a proxy that no longer answers.
				m.set(func() { m.connected, m.errText = false, herr.Error() })
				return gotAny.Load(), herr
			}
		}
	}
	if cerr := conn.Err(); cerr != nil {
		return gotAny.Load(), cerr
	}
	return gotAny.Load(), fmt.Errorf("heart rate monitor connection closed")
}
