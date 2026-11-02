package ridectl

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/esphomeapi"
)

const (
	diagOpenBound  = 8 * time.Second  // opening the adapter or the proxy link
	diagProxyBound = 3 * time.Second  // one ProxyStatus query
	maxDiagScan    = 10 * time.Second // longest on-demand scan
)

// Diagnostics is a Bluetooth health report. It never carries a secret.
type Diagnostics struct {
	Transport string       `json:"transport" enum:"local,esphome,none" doc:"Bluetooth path that was checked: local (this host's adapter), esphome (a Bluetooth proxy) or none when neither the bike nor the heart rate monitor uses Bluetooth."`
	Local     *LocalDiag   `json:"local,omitempty" doc:"Adapter check; present when transport is local."`
	Proxy     *ProxyDiag   `json:"proxy,omitempty" doc:"Proxy check; present when transport is esphome."`
	Devices   []DeviceDiag `json:"devices" doc:"The configured Bluetooth bike and heart rate monitor."`
	CheckedAt time.Time    `json:"checked_at" doc:"When the check ran (RFC 3339)."`
}

// LocalDiag is the result of opening this host's Bluetooth adapter.
type LocalDiag struct {
	Status  string `json:"status" enum:"ok,error" doc:"ok when the adapter opened, otherwise error."`
	Code    string `json:"code,omitempty" doc:"Machine-readable cause when status is error: dbus_socket_missing, dbus_denied, bluez_not_running, no_adapter, adapter_error, or busy (a scan or another diagnostics run holds the adapter)."`
	Message string `json:"message" doc:"What was found, or the raw error."`
	Fix     string `json:"fix,omitempty" doc:"Exact steps that fix the problem; absent when status is ok or the cause is unknown."`
}

// ProxyDiag is the result of querying the ESPHome Bluetooth proxy.
type ProxyDiag struct {
	Status       string   `json:"status" enum:"ok,error" doc:"ok when the proxy is usable, otherwise error."`
	Code         string   `json:"code,omitempty" doc:"Machine-readable cause when status is error: no_host, unreachable, bad_key, plaintext, no_proxy, held, no_slots, or busy (a scan or another diagnostics run holds the proxy)."`
	Message      string   `json:"message" doc:"What was found, or the raw error."`
	Fix          string   `json:"fix,omitempty" doc:"Exact steps that fix the problem; absent when status is ok."`
	Name         string   `json:"name,omitempty" doc:"The device's ESPHome name."`
	MAC          string   `json:"mac,omitempty" doc:"The device's MAC address."`
	Version      string   `json:"version,omitempty" doc:"ESPHome version running on the device."`
	Flags        []string `json:"flags" doc:"Bluetooth proxy features the firmware reports: passive_scan, active_connections, remote_caching, pairing, cache_clearing, raw_advertisements, state_and_mode."`
	MissingFlags []string `json:"missing_flags" doc:"Features Wattson needs that the firmware lacks: active_connections and raw_advertisements."`
	Subscription string   `json:"subscription" enum:"ok,held_by_other,unknown" doc:"ok while Wattson receives the proxy's Bluetooth data; held_by_other when another client (usually Home Assistant) took it; unknown when Wattson has no connection open yet, so it just subscribed and cannot tell."`
	Free         int      `json:"free" doc:"Free connection slots; 0 together with limit 0 when the proxy did not report its slots in time."`
	Limit        int      `json:"limit" doc:"Total connection slots; 0 when the proxy did not report them in time."`
	Allocated    []string `json:"allocated" doc:"Addresses of the devices holding a slot."`
}

// DeviceDiag is one configured Bluetooth device.
type DeviceDiag struct {
	Role      string `json:"role" enum:"bike,hr" doc:"bike or hr (heart rate monitor)."`
	Address   string `json:"address" doc:"Configured Bluetooth address; empty when none is set."`
	State     string `json:"state" doc:"idle when no bike is connected, otherwise the connection state (bike: connecting, ready, riding, paused or finished; heart rate monitor: connected or searching)."`
	Connected bool   `json:"connected" doc:"Whether a live link to the device exists right now."`
	RSSI      *int   `json:"rssi,omitempty" doc:"Signal strength in dBm seen during this check's scan; absent when the device was not seen or no scan was requested."`
	Error     string `json:"error,omitempty" doc:"Most recent connection problem."`
	Hint      string `json:"hint,omitempty" doc:"Advice for fixing the problem."`
}

// Fix texts, exact.
const (
	fixDBusSocket  = "Mount the host's D-Bus socket: under `volumes:` for wattson in compose.yml add `/run/dbus/system_bus_socket:/run/dbus/system_bus_socket:ro`, then run `docker compose up -d`."
	fixDBusDenied  = "Run `getent group bluetooth | cut -d: -f3` on the host, put that number under `group_add:` for wattson in compose.yml, then run `docker compose up -d`."
	fixBlueZ       = "BlueZ is not running on the host. Run `sudo systemctl enable --now bluetooth` on the host."
	fixNoAdapter   = "No Bluetooth adapter found on the host. Check `bluetoothctl list` on the host, plug in a USB adapter, or switch the transport to an ESPHome proxy."
	fixUnreachable = "Check the proxy host and that port 6053 is reachable from the Wattson container."
	fixBadKey      = "The key must match `api: encryption: key:` in the proxy YAML (esphome/wattson-proxy.yaml)."
	fixPlaintext   = "Enable `api: encryption:` in the proxy YAML."
	fixNoProxy     = "Set `bluetooth_proxy: active: true` in the proxy YAML and flash it."
	fixNoSlots     = "All connection slots are in use. Set `connection_slots: 3` in the proxy YAML."
	fixScanRunning = "Wait for the running scan or diagnostics to finish, then run the diagnostics again."
	fixNoHost      = "Enter the ESPHome proxy host (host or host:port) in the Bluetooth settings."
	msgScanRunning = "A Bluetooth scan or another diagnostics run is in progress, and one adapter or proxy cannot serve two at once."
)

// classifyLocal names why opening or using the local adapter failed and the
// exact fix. It works from the error text and the D-Bus error name.
func classifyLocal(err error) (code, fix string) {
	var de dbus.Error
	name := ""
	if errors.As(err, &de) {
		name = de.Name
	}
	low := strings.ToLower(err.Error() + " " + name)
	switch {
	case strings.Contains(low, "system_bus_socket") && strings.Contains(low, "no such file"):
		return "dbus_socket_missing", fixDBusSocket
	case strings.Contains(low, "accessdenied") || strings.Contains(low, "notauthorized") ||
		strings.Contains(low, "access denied") || strings.Contains(low, "permission denied"):
		return "dbus_denied", fixDBusDenied
	case strings.Contains(low, "serviceunknown") || strings.Contains(low, "org.bluez was not provided"):
		return "bluez_not_running", fixBlueZ
	case strings.Contains(low, "unknownobject") || strings.Contains(low, "no such object") ||
		(strings.Contains(low, "hci0") && strings.Contains(low, "not found")):
		return "no_adapter", fixNoAdapter
	}
	return "adapter_error", ""
}

// classifyProxy names why reaching the ESPHome proxy failed.
func classifyProxy(err error) (code, fix string) {
	switch {
	case errors.Is(err, ErrNoHost):
		return "no_host", fixNoHost
	case errors.Is(err, esphomeapi.ErrBadKey), errors.Is(err, esphomeapi.ErrHandshake):
		return "bad_key", fixBadKey
	case strings.Contains(strings.ToLower(err.Error()), "plaintext"):
		return "plaintext", fixPlaintext
	}
	return "unreachable", fixUnreachable
}

var flagNames = []struct {
	bit  uint32
	name string
}{
	{esphomeapi.BTFlagPassiveScan, "passive_scan"},
	{esphomeapi.BTFlagActiveConnections, "active_connections"},
	{esphomeapi.BTFlagRemoteCaching, "remote_caching"},
	{esphomeapi.BTFlagPairing, "pairing"},
	{esphomeapi.BTFlagCacheClearing, "cache_clearing"},
	{esphomeapi.BTFlagRawAdvertisements, "raw_advertisements"},
	{esphomeapi.BTFlagStateAndMode, "state_and_mode"},
}

const requiredFlags = esphomeapi.BTFlagActiveConnections | esphomeapi.BTFlagRawAdvertisements

// diagTarget is what one diagnosis checks: the live connection's plan, or the
// saved settings when idle.
type diagTarget struct {
	kind, host, key string
	bike, hr        string // configured addresses; "" when unset
	bikeBLE, hrBLE  bool
}

// scrub removes secret from text (a short secret would mangle unrelated text).
func scrub(text, secret string) string {
	if len(secret) >= 8 {
		return strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}

// Diagnose checks the Bluetooth path the bike and heart rate monitor use.
// While a bike is connected (or connecting) it reuses that connection's shared
// transport: a second ESPHome subscription would take the proxy from the
// connection itself. When idle it opens a temporary transport, closed before
// it returns, and holds the scan slot meanwhile so a Connect or Scan arriving
// at the same time gets bike_busy instead of fighting it for the proxy; a
// Diagnose that finds a scan running reports "busy" instead of failing. scan
// is the length of the passive scan that fills in the devices' RSSI (0 skips
// it; at most 10 s). It never returns ErrBusy.
func (m *Manager) Diagnose(ctx context.Context, scan time.Duration) Diagnostics {
	scan = max(0, min(scan, maxDiagScan))
	d := Diagnostics{Transport: "none", Devices: []DeviceDiag{}, CheckedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, diagOpenBound+diagProxyBound+scan+time.Second)
	defer cancel()
	stop := context.AfterFunc(m.root, cancel) // closing the manager ends the check
	defer stop()

	s, err := m.d.Settings(ctx)
	if err != nil {
		m.log.Warn("diagnostics: settings unavailable", "err", err)
		return d
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return d
	}
	c := m.cur
	st := m.status
	var t diagTarget
	var shared *sharedTransport
	if c != nil {
		t = diagTarget{kind: c.plan.transport, host: c.plan.host, key: c.plan.key,
			bikeBLE: c.plan.bleBike(), hrBLE: c.plan.hrSource == "ble"}
		if t.bikeBLE {
			t.bike = c.plan.addr.String()
		}
		if t.hrBLE {
			t.hr = c.plan.hrAddr.String()
		}
		shared, _ = c.tr.(*sharedTransport)
	} else {
		b := s.Bike
		t = diagTarget{kind: b.Transport, host: b.ESPHomeHost, key: b.ESPHomeKey,
			bikeBLE: b.Driver != "sim" && b.Driver != "ha", hrBLE: s.HeartRate.Source == "ble"}
		if t.bikeBLE {
			t.bike = canonAddr(b.Address)
		}
		if t.hrBLE {
			t.hr = canonAddr(s.HeartRate.Address)
		}
	}
	busy := false
	if c == nil && (t.bikeBLE || t.hrBLE) {
		if m.scanning {
			busy = true
		} else {
			m.scanning = true
			m.wg.Add(1)
			defer func() {
				m.mu.Lock()
				m.scanning = false
				m.mu.Unlock()
				m.wg.Done()
			}()
		}
	}
	m.mu.Unlock()

	if !t.bikeBLE && !t.hrBLE {
		return d // nothing uses Bluetooth
	}
	if t.kind == "" {
		t.kind = "local"
	}
	d.Transport = t.kind
	if t.kind != "esphome" {
		d.Transport = "local"
	}

	// Open the transport: the live shared one, else a temporary one.
	octx, ocancel := context.WithTimeout(ctx, diagOpenBound)
	var tr blue.Transport
	var openErr error
	switch {
	case busy:
		openErr = errBusyScan
	case c != nil && shared == nil:
		// A live connection always has its shared transport; never open a
		// temporary one next to it (it would take the proxy from the connection).
		openErr = errors.New("the bike connection has no shared Bluetooth transport")
	case shared != nil:
		if _, openErr = shared.get(octx); openErr == nil {
			tr = shared
		}
	default:
		tr, openErr = m.openTransport(octx, t.kind, t.host, t.key)
		if tr != nil {
			defer tr.Close()
		}
	}
	ocancel()
	live := shared != nil

	var rssi map[[6]byte]int
	var scanErr error
	if openErr == nil && scan > 0 {
		rssi, scanErr = scanRSSI(ctx, tr, scan)
	}

	own := []blue.Address{}
	for _, a := range []string{t.bike, t.hr} {
		if x, err := blue.ParseAddress(a); err == nil {
			own = append(own, x)
		}
	}
	switch d.Transport {
	case "local":
		d.Local = localDiag(openErr, scanErr)
	default:
		d.Proxy = m.proxyDiag(ctx, tr, live, openErr, scanErr, own, t.key)
	}

	d.Devices = m.deviceDiags(t, c != nil, st, rssi)
	return d
}

var errBusyScan = errors.New("busy")

// canonAddr normalises a configured address for display; unparsable text is
// shown as typed.
func canonAddr(s string) string {
	s = strings.TrimSpace(s)
	if a, err := blue.ParseAddress(s); err == nil {
		return a.String()
	}
	return s
}

func localDiag(openErr, scanErr error) *LocalDiag {
	err := openErr
	if err == nil {
		err = scanErr
	}
	switch {
	case err == nil:
		return &LocalDiag{Status: "ok", Message: "The Bluetooth adapter opened."}
	case errors.Is(err, errBusyScan):
		return &LocalDiag{Status: "error", Code: "busy", Message: msgScanRunning, Fix: fixScanRunning}
	}
	code, fix := classifyLocal(err)
	return &LocalDiag{Status: "error", Code: code, Message: err.Error(), Fix: fix}
}

func (m *Manager) proxyDiag(ctx context.Context, tr blue.Transport, live bool, openErr, scanErr error, own []blue.Address, key string) *ProxyDiag {
	p := &ProxyDiag{Status: "error", Flags: []string{}, MissingFlags: []string{}, Subscription: "unknown", Allocated: []string{}}
	fail := func(err error) *ProxyDiag {
		p.Code, p.Fix = classifyProxy(err)
		p.Message = scrub(err.Error(), key)
		return p
	}
	if errors.Is(openErr, errBusyScan) {
		p.Code, p.Message, p.Fix = "busy", msgScanRunning, fixScanRunning
		return p
	}
	if openErr != nil {
		return fail(openErr)
	}
	px, ok := tr.(blue.Proxy)
	if !ok {
		p.Status, p.Message = "ok", "The transport reports no proxy details."
		return p
	}
	qctx, cancel := context.WithTimeout(ctx, cmp.Or(m.diagProxy, diagProxyBound))
	defer cancel()
	ps, err := px.ProxyStatus(qctx)
	if errors.Is(err, errNotProxy) {
		p.Status, p.Message = "ok", "The transport reports no proxy details."
		return p
	}
	p.Name, p.MAC, p.Version = ps.Name, ps.MAC, ps.Version
	for _, f := range flagNames {
		if ps.Flags&f.bit != 0 {
			p.Flags = append(p.Flags, f.name)
		}
		if requiredFlags&f.bit != 0 && ps.Flags&f.bit == 0 {
			p.MissingFlags = append(p.MissingFlags, f.name)
		}
	}
	if err != nil {
		return fail(err)
	}
	p.Free, p.Limit = ps.Free, ps.Limit
	holdsSlot := false
	for _, a := range ps.Allocated {
		p.Allocated = append(p.Allocated, a.String())
		for _, o := range own {
			holdsSlot = holdsSlot || a.MAC == o.MAC
		}
	}
	if live {
		p.Subscription = "ok"
		if !ps.Subscribed {
			p.Subscription = "held_by_other"
		}
	}
	switch {
	case len(p.MissingFlags) > 0:
		p.Code, p.Fix = "no_proxy", fixNoProxy
		p.Message = "The firmware lacks Bluetooth proxy features Wattson needs: " + strings.Join(p.MissingFlags, ", ") + "."
	case p.Subscription == "held_by_other":
		p.Code, p.Fix = "held", hintFor(blue.ErrProxyHeld)
		p.Message = "Another client holds this proxy's Bluetooth subscription, so it sends Wattson nothing."
	case ps.Limit > 0 && ps.Free == 0 && !holdsSlot:
		p.Code, p.Fix = "no_slots", fixNoSlots
		p.Message = "The proxy has no free connection slots."
	case scanErr != nil:
		return fail(scanErr)
	case ps.Limit == 0: // ConnectionsFree was not answered in time: slots unknown
		p.Status, p.Message = "ok", "The proxy is reachable and usable; it did not report its connection slots in time."
	default:
		p.Status, p.Message = "ok", "The proxy is reachable and usable."
	}
	return p
}

// scanRSSI scans for d and returns the strongest RSSI seen per MAC. An error
// is only a failure to scan, not the scan's own timeout.
func scanRSSI(ctx context.Context, tr blue.Transport, d time.Duration) (map[[6]byte]int, error) {
	sctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var mu sync.Mutex
	seen := map[[6]byte]int{}
	err := tr.Scan(sctx, func(a blue.Advertisement) {
		mu.Lock()
		defer mu.Unlock()
		if prev, ok := seen[a.Addr.MAC]; !ok || a.RSSI > prev {
			seen[a.Addr.MAC] = a.RSSI
		}
	})
	if ctx.Err() != nil {
		err = nil // the whole check ran out of time: report what was seen
	}
	mu.Lock()
	defer mu.Unlock()
	return seen, err
}

func (m *Manager) deviceDiags(t diagTarget, live bool, st Status, rssi map[[6]byte]int) []DeviceDiag {
	rssiOf := func(addr string) *int {
		a, err := blue.ParseAddress(addr)
		if err != nil {
			return nil
		}
		if v, ok := rssi[a.MAC]; ok {
			return &v
		}
		return nil
	}
	out := []DeviceDiag{}
	if t.bikeBLE {
		dev := DeviceDiag{Role: "bike", Address: t.bike, State: "idle", RSSI: rssiOf(t.bike)}
		if live {
			dev.State, dev.Connected, dev.Error, dev.Hint = st.State, st.Connected, st.LastError, st.Hint
		}
		out = append(out, dev)
	}
	if t.hrBLE {
		dev := DeviceDiag{Role: "hr", Address: t.hr, State: "idle", RSSI: rssiOf(t.hr)}
		if live {
			dev.State = "searching"
			if st.HR != nil {
				dev.Connected, dev.Error = st.HR.Connected, st.HR.Error
				if st.HR.Connected {
					dev.State = "connected"
				}
			}
		}
		out = append(out, dev)
	}
	return out
}
