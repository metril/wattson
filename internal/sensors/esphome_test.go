package sensors_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
	"github.com/metril/wattson/internal/sensors"
)

var (
	bikeAddr = blue.Address{MAC: [6]byte{1, 2, 3, 4, 5, 6}, Type: blue.AddrRandom}
	hrDev    = blue.Address{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x01}}
)

const hrNotifyHandle = 3

// proxyEnv is an ESPHome proxy with a FitPlus bike and a heart rate monitor.
type proxyEnv struct {
	srv *esphometest.Server
	tr  blue.Transport
}

func adv(name string, svc uint16) []byte {
	d := append([]byte{byte(len(name) + 1), 0x09}, name...)
	return append(d, 3, 0x03, byte(svc), byte(svc>>8))
}

func newProxyEnv(t *testing.T, hrOnProxy bool) proxyEnv {
	t.Helper()
	k := bytes.Repeat([]byte{9}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	srv.AddBLEDevice(esphometest.RawAdv{Address: bikeAddr.Uint64(), RSSI: -60, AddressType: 1, Data: adv("MRK-S29R2", 0xfff0)},
		[]esphometest.GATTService{{UUID: "fff0", Handle: 1, Chars: []esphometest.GATTChar{
			{UUID: "fff1", Handle: 3, Props: uint32(blue.PropNotify)},
			{UUID: "fff2", Handle: 6, Props: uint32(blue.PropWrite | blue.PropWriteNoResp)},
		}}})
	if hrOnProxy {
		srv.AddBLEDevice(esphometest.RawAdv{Address: hrDev.Uint64(), RSSI: -55, Data: adv("HRM", 0x180d)},
			[]esphometest.GATTService{{UUID: "180d", Handle: 1, Chars: []esphometest.GATTChar{
				{UUID: "2a37", Handle: hrNotifyHandle, Props: uint32(blue.PropNotify)},
			}}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tr, err := blue.NewESPHomeWith(ctx, srv.Addr(), base64.StdEncoding.EncodeToString(k), blue.WithProbeTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return proxyEnv{srv, tr}
}

func poll(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startHR(t *testing.T, cfg sensors.BLEConfig) *sensors.BLEMonitor {
	t.Helper()
	m := sensors.NewBLE(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return m
}

// The bike and the heart rate monitor use separate connection slots on one
// proxy and one transport; losing HR's slot never touches the bike.
func TestHRAndBikeShareProxy(t *testing.T) {
	t.Parallel()
	e := newProxyEnv(t, true)
	sess, err := bike.BLEDial(e.tr, bikeAddr, bike.Options{Keepalive: time.Hour})(context.Background())
	if err != nil {
		t.Fatalf("bike dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	m := startHR(t, sensors.BLEConfig{Transport: e.tr, Addr: hrDev, BackoffMin: 20 * time.Millisecond, BackoffMax: 20 * time.Millisecond})
	poll(t, "HR connected", func() bool { return m.Status().Connected })
	e.srv.NotifyBLE(hrDev.Uint64(), hrNotifyHandle, []byte{0x00, 0x48})
	poll(t, "HR 72", func() bool { b, _ := m.Latest(); return b == 72 })

	seen := map[uint64]bool{}
	for _, r := range e.srv.BLEConnRequests() {
		seen[r.Address] = true
	}
	if !seen[bikeAddr.Uint64()] || !seen[hrDev.Uint64()] {
		t.Fatalf("connect requests %+v lack the bike or the heart rate monitor", e.srv.BLEConnRequests())
	}

	// DropBLE frees the slot, so take every slot away again after each drop
	// until the monitor reports the shortage.
	poll(t, "HR reports no free slot", func() bool {
		e.srv.DropBLE(hrDev.Uint64())
		e.srv.SetFreeSlots(0)
		return strings.Contains(m.Status().Error, "no free")
	})
	select {
	case <-sess.Done():
		t.Fatal("bike session ended when the heart rate connection was lost")
	default:
	}
	if m.Status().Connected {
		t.Fatal("HR still Connected")
	}
}

// Another client holding the proxy's Bluetooth subscription: the HR monitor
// says so (with a hint) instead of hanging, and never re-subscribes.
func TestHRProxyHeldAtStart(t *testing.T) {
	t.Parallel()
	e := newProxyEnv(t, true)
	e.srv.SetForeignSubscriber()
	m := startHR(t, sensors.BLEConfig{Transport: e.tr, Addr: hrDev, BackoffMin: 20 * time.Millisecond, BackoffMax: 50 * time.Millisecond})
	poll(t, "ErrProxyHeld in status", func() bool { return strings.Contains(m.Status().Error, "another client") })
	if st := m.Status(); st.Connected || !strings.Contains(st.Error, blue.ErrProxyHeld.Error()) {
		t.Fatalf("status = %+v", st)
	}
	if f := e.srv.BLEAdvFlags(); len(f) != 1 {
		t.Fatalf("advert subscriptions = %v: Wattson must not take the proxy back", f)
	}
}

// Losing the subscription to another client mid-connection is detected.
func TestHRProxyTakenOverWhileConnected(t *testing.T) {
	t.Parallel()
	e := newProxyEnv(t, true)
	m := startHR(t, sensors.BLEConfig{Transport: e.tr, Addr: hrDev, BackoffMin: 20 * time.Millisecond, BackoffMax: 50 * time.Millisecond, ProxyCheck: 50 * time.Millisecond})
	poll(t, "HR connected", func() bool { return m.Status().Connected })
	e.srv.SetForeignSubscriber()
	t.Cleanup(func() { e.tr.Close() }) // a proxy that no longer answers would stall the monitor's disconnect
	poll(t, "takeover reported", func() bool { return strings.Contains(m.Status().Error, "another client") })
	if m.Status().Connected {
		t.Fatal("still Connected after losing the subscription")
	}
}
