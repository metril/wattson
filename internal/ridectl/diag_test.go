package ridectl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
	"github.com/metril/wattson/internal/settings"
)

func TestClassifyLocal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantFix  string
	}{
		{"socket", errors.New("dial unix /run/dbus/system_bus_socket: connect: no such file or directory"), "dbus_socket_missing", fixDBusSocket},
		{"denied", dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}, "dbus_denied", fixDBusDenied},
		{"denied wrapped", fmt.Errorf("scan: %w: add the container user", dbus.Error{Name: "org.bluez.Error.NotAuthorized"}), "dbus_denied", fixDBusDenied},
		{"permission", errors.New("open: permission denied"), "dbus_denied", fixDBusDenied},
		{"bluez", errors.New("The name org.bluez was not provided by any .service files"), "bluez_not_running", fixBlueZ},
		{"service unknown", dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}, "bluez_not_running", fixBlueZ},
		{"adapter", dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject"}, "no_adapter", fixNoAdapter},
		{"hci0", errors.New("hci0 not found"), "no_adapter", fixNoAdapter},
		{"other", errors.New("boom"), "adapter_error", ""},
	}
	for _, c := range cases {
		code, fix := classifyLocal(c.err)
		if code != c.wantCode || fix != c.wantFix {
			t.Errorf("%s: classifyLocal = %q, %q; want %q, %q", c.name, code, fix, c.wantCode, c.wantFix)
		}
	}
	// The fix texts are part of the contract.
	for got, want := range map[string]string{
		fixDBusSocket:  "Mount the host's D-Bus socket: under `volumes:` for wattson in compose.yml add `/run/dbus/system_bus_socket:/run/dbus/system_bus_socket:ro`, then run `docker compose up -d`.",
		fixDBusDenied:  "Run `getent group bluetooth | cut -d: -f3` on the host, put that number under `group_add:` for wattson in compose.yml, then run `docker compose up -d`.",
		fixBlueZ:       "BlueZ is not running on the host. Run `sudo systemctl enable --now bluetooth` on the host.",
		fixNoAdapter:   "No Bluetooth adapter found on the host. Check `bluetoothctl list` on the host, plug in a USB adapter, or switch the transport to an ESPHome proxy.",
		fixUnreachable: "Check the proxy host and that port 6053 is reachable from the Wattson container.",
		fixBadKey:      "The key must match `api: encryption: key:` in the proxy YAML (esphome/wattson-proxy.yaml).",
		fixPlaintext:   "Enable `api: encryption:` in the proxy YAML.",
		fixNoProxy:     "Set `bluetooth_proxy: active: true` in the proxy YAML and flash it.",
		fixNoSlots:     "All connection slots are in use. Set `connection_slots: 3` in the proxy YAML.",
	} {
		if got != want {
			t.Errorf("fix text %q != %q", got, want)
		}
	}
}

func bleBike(extra func(*settings.Settings)) func(*settings.Settings) {
	return func(s *settings.Settings) {
		s.Bike.Driver, s.Bike.Address = "fitplus", testAddr.String()
		if extra != nil {
			extra(s)
		}
	}
}

func TestDiagnoseLocalOK(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		hr := hrDevice()
		hr.Adv.RSSI = -58
		fake := newSpy(fitPlusEcho(hr))
		m := newMgr(nil, 20*time.Millisecond, bleBike(withHR), Deps{OpenLocal: func() (blue.Transport, error) { return fake, nil }})
		defer m.Close(context.Background())
		start(t, m)
		waitFor(t, 5*time.Second, "HR connected", func() bool { h := m.Status().HR; return h != nil && h.Connected })

		d := m.Diagnose(t.Context(), 4*time.Second)
		if d.Transport != "local" || d.Local == nil || d.Local.Status != "ok" || d.Proxy != nil {
			t.Fatalf("diagnostics = %+v / %+v", d, d.Local)
		}
		if len(d.Devices) != 2 || d.Devices[0].Role != "bike" || d.Devices[1].Role != "hr" {
			t.Fatalf("devices = %+v", d.Devices)
		}
		bk, h := d.Devices[0], d.Devices[1]
		if !bk.Connected || bk.State != "ready" || bk.Address != testAddr.String() {
			t.Errorf("bike = %+v", bk)
		}
		if !h.Connected || h.State != "connected" || h.RSSI == nil || *h.RSSI != -58 {
			t.Errorf("hr = %+v (rssi %v)", h, h.RSSI)
		}
		if bk.RSSI == nil || *bk.RSSI != -50 {
			t.Errorf("bike rssi = %v", bk.RSSI)
		}
		fake.mu.Lock()
		closes := fake.closes
		fake.mu.Unlock()
		if closes != 0 {
			t.Errorf("the live transport was closed %d times", closes)
		}
		// Without a scan nothing is "seen".
		if d := m.Diagnose(t.Context(), 0); d.Devices[1].RSSI != nil {
			t.Errorf("rssi without a scan = %v", *d.Devices[1].RSSI)
		}
	})
}

func TestDiagnoseLocalDenied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := newMgr(nil, 20*time.Millisecond, bleBike(nil), Deps{OpenLocal: func() (blue.Transport, error) {
			return nil, fmt.Errorf("scan: %w", dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"})
		}})
		defer m.Close(context.Background())
		d := m.Diagnose(t.Context(), 0)
		if d.Local == nil || d.Local.Status != "error" || d.Local.Code != "dbus_denied" || d.Local.Fix != fixDBusDenied {
			t.Fatalf("local = %+v", d.Local)
		}
		if len(d.Devices) != 1 || d.Devices[0].State != "idle" || d.Devices[0].Connected {
			t.Fatalf("devices = %+v", d.Devices)
		}
	})
}

func TestDiagnoseNoBluetooth(t *testing.T) {
	t.Parallel()
	m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{})
	defer m.Close(context.Background())
	d := m.Diagnose(t.Context(), 0)
	if d.Transport != "none" || d.Local != nil || d.Proxy != nil || d.Devices == nil || len(d.Devices) != 0 {
		t.Fatalf("diagnostics = %+v", d)
	}
}

// Idle: a temporary transport, opened once and closed before Diagnose returns.
func TestDiagnoseIdleTempTransport(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		spy := newSpy(bluetest.NewFake())
		opens := 0
		m := newMgr(nil, 20*time.Millisecond, bleBike(nil), Deps{OpenLocal: func() (blue.Transport, error) { opens++; return spy, nil }})
		defer m.Close(context.Background())
		d := m.Diagnose(t.Context(), 1*time.Second)
		if d.Local == nil || d.Local.Status != "ok" {
			t.Fatalf("local = %+v", d.Local)
		}
		spy.mu.Lock()
		closes := spy.closes
		spy.mu.Unlock()
		if opens != 1 || closes != 1 {
			t.Fatalf("opens = %d, closes = %d, want 1 and 1", opens, closes)
		}
		if st := m.Status(); st.State != "idle" {
			t.Fatalf("status = %+v", st)
		}
		// The manager is free again: a connect is not refused.
		if _, err := m.Connect(t.Context(), testRider); errors.Is(err, ErrBusy) {
			t.Fatalf("Connect after Diagnose = %v", err)
		}
	})
}

// A running scan owns the adapter: Diagnose reports it instead of failing.
func TestDiagnoseWhileScanning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := &advTransport{}
		m := newMgr(nil, 20*time.Millisecond, bleBike(nil), Deps{OpenLocal: func() (blue.Transport, error) { return tr, nil }})
		defer m.Close(context.Background())
		go func() { _, _ = m.Scan(t.Context(), ScanRequest{DurationS: 5}) }()
		synctest.Wait()
		d := m.Diagnose(t.Context(), 0)
		if d.Local == nil || d.Local.Status != "error" || d.Local.Code != "busy" {
			t.Fatalf("local = %+v", d.Local)
		}
		time.Sleep(6 * time.Second)
		synctest.Wait()
		if d := m.Diagnose(t.Context(), 0); d.Local.Status != "ok" {
			t.Fatalf("after the scan: %+v", d.Local)
		}
	})
}

func TestDiagnoseESPHome(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	srv.HandleHello(0x23)
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{})
	defer m.Close(context.Background())
	start(t, m)
	poll(t, 2*time.Second, "bike holds a slot", func() bool { return srv.BLEConnected(testAddr.Uint64()) })

	var d Diagnostics
	poll(t, 2*time.Second, "slot reported", func() bool {
		d = m.Diagnose(t.Context(), 0)
		return d.Proxy != nil && d.Proxy.Free == 2
	})
	p := d.Proxy
	if d.Transport != "esphome" || d.Local != nil || p.Status != "ok" || p.Code != "" {
		t.Fatalf("diagnostics = %+v / %+v", d, p)
	}
	if !reflect.DeepEqual(p.Flags, []string{"passive_scan", "active_connections", "raw_advertisements"}) || len(p.MissingFlags) != 0 || p.MissingFlags == nil {
		t.Errorf("flags = %v missing = %#v", p.Flags, p.MissingFlags)
	}
	if p.Subscription != "ok" || p.Free != 2 || p.Limit != 3 || p.Name == "" || p.MAC == "" || p.Version == "" {
		t.Errorf("proxy = %+v", p)
	}
	if !reflect.DeepEqual(p.Allocated, []string{blue.Address{MAC: testAddr.MAC}.String()}) {
		t.Errorf("allocated = %v", p.Allocated)
	}
	if n := len(srv.BLEAdvFlags()); n != 1 {
		t.Errorf("advert subscriptions = %d: Diagnose must not open a second one", n)
	}
}

func TestDiagnoseProxyHeld(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{
		OpenESPHome: func(ctx context.Context, hp, k string) (blue.Transport, error) {
			tr, err := blue.NewESPHomeWith(ctx, hp, k, blue.WithHeldAfter(20*time.Millisecond))
			if err != nil {
				return nil, err
			}
			if _, err := tr.(blue.Proxy).ProxyStatus(ctx); err != nil { // the proxy has processed our subscription
				return nil, err
			}
			srv.SetForeignSubscriber()
			time.Sleep(40 * time.Millisecond)
			return tr, nil
		},
	})
	defer m.Close(context.Background())
	if _, err := m.Connect(t.Context(), testRider); err != nil {
		t.Fatal(err)
	}
	poll(t, 2*time.Second, "held hint", func() bool { return strings.HasPrefix(m.Status().Hint, "Another client") })
	d := m.Diagnose(t.Context(), 0)
	p := d.Proxy
	if p == nil || p.Status != "error" || p.Code != "held" || p.Subscription != "held_by_other" || p.Fix != hintFor(blue.ErrProxyHeld) {
		t.Fatalf("proxy = %+v", p)
	}
	if len(d.Devices) != 1 || d.Devices[0].Connected || d.Devices[0].Hint == "" {
		t.Errorf("devices = %+v", d.Devices)
	}
	if f := srv.BLEAdvFlags(); len(f) != 1 {
		t.Fatalf("advert subscriptions = %v: Diagnose must not take the proxy back", f)
	}
}

func TestDiagnoseProxyMissingFlags(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	srv.HandleHello(0x01) // passive scan only
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{})
	defer m.Close(context.Background())
	p := m.Diagnose(t.Context(), 0).Proxy
	if p == nil || p.Code != "no_proxy" || p.Fix != fixNoProxy || !reflect.DeepEqual(p.MissingFlags, []string{"active_connections", "raw_advertisements"}) {
		t.Fatalf("proxy = %+v", p)
	}
	// Idle: a temporary subscription proves nothing about other clients.
	if p.Subscription != "unknown" {
		t.Errorf("subscription = %q", p.Subscription)
	}
}

func TestDiagnoseProxyNoSlots(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	srv.SetFreeSlots(0)
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{})
	defer m.Close(context.Background())
	p := m.Diagnose(t.Context(), 0).Proxy
	if p == nil || p.Code != "no_slots" || p.Fix != fixNoSlots {
		t.Fatalf("proxy = %+v", p)
	}
}

// A proxy that does not report its slots in time is not "out of slots".
func TestDiagnoseProxySlotsUnknown(t *testing.T) {
	t.Parallel()
	srv, key := proxyServer(t)
	// Swallow the connections-free request so ProxyStatus times out.
	srv.Handle(80, func([]byte) []esphometest.Reply { return nil })
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, key, false), Deps{})
	m.diagProxy = 100 * time.Millisecond
	defer m.Close(context.Background())
	p := m.Diagnose(t.Context(), 0).Proxy
	if p == nil || p.Status != "ok" || p.Code != "" || p.Fix != "" || p.Free != 0 || p.Limit != 0 {
		t.Fatalf("proxy = %+v", p)
	}
}

// HR over ESPHome with no host configured points at the missing setting.
func TestDiagnoseHROnlyNoHost(t *testing.T) {
	t.Parallel()
	m := newMgr(&simRec{}, 20*time.Millisecond, func(s *settings.Settings) {
		withHR(s)
		s.Bike.Transport, s.Bike.ESPHomeHost = "esphome", ""
	}, Deps{})
	defer m.Close(context.Background())
	d := m.Diagnose(t.Context(), 0)
	if d.Transport != "esphome" || d.Proxy == nil || d.Proxy.Code != "no_host" || d.Proxy.Fix != fixNoHost {
		t.Fatalf("diagnostics = %+v / %+v", d, d.Proxy)
	}
}

func TestDiagnoseESPHomeBadKey(t *testing.T) {
	t.Parallel()
	srv, _ := proxyServer(t)
	wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	m := newMgr(nil, 20*time.Millisecond, espSettings(srv, wrong, false), Deps{})
	defer m.Close(context.Background())
	d := m.Diagnose(t.Context(), 0)
	if d.Proxy == nil || d.Proxy.Status != "error" || d.Proxy.Code != "bad_key" || d.Proxy.Fix != fixBadKey {
		t.Fatalf("proxy = %+v", d.Proxy)
	}
	// The key never appears in the report.
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), wrong) {
		t.Fatalf("diagnostics leak the key: %s", b)
	}
}

func TestDiagnoseNoSecrets(t *testing.T) {
	t.Parallel()
	const key = "SECRETKEYSECRETKEYSECRETKEY=="
	m := newMgr(nil, 20*time.Millisecond, func(s *settings.Settings) {
		bleBike(nil)(s)
		s.Bike.Transport, s.Bike.ESPHomeHost, s.Bike.ESPHomeKey = "esphome", "h", key
	}, Deps{OpenESPHome: func(context.Context, string, string) (blue.Transport, error) {
		return nil, fmt.Errorf("dial failed with key %s", key)
	}})
	defer m.Close(context.Background())
	d := m.Diagnose(t.Context(), 0)
	b, _ := json.Marshal(d)
	if d.Proxy == nil || d.Proxy.Code != "unreachable" || strings.Contains(string(b), key) {
		t.Fatalf("proxy = %+v, json %s", d.Proxy, b)
	}
}
