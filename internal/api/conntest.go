package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/esphomeapi"
	"github.com/metril/wattson/internal/ridectl"
)

// connTestTimeout bounds every connection test.
const connTestTimeout = 5 * time.Second

const esphomeDefaultPort = "6053"

// TestResult is the outcome of a connection test. A failed test is still
// an HTTP 200 with ok false.
type TestResult struct {
	OK        bool   `json:"ok" doc:"Whether the connection works."`
	Message   string `json:"message" doc:"Human-readable outcome or error text."`
	LatencyMS int    `json:"latency_ms" doc:"Time the test took, in milliseconds."`
}

type testHAIn struct {
	Body struct {
		URL   string `json:"url,omitempty" doc:"Home Assistant base URL to test. Empty uses the stored URL."`
		Token string `json:"token,omitempty" doc:"Long-lived access token to test. Empty uses the stored token."`
	}
}

type testESPHomeIn struct {
	Body struct {
		Host string `json:"host,omitempty" doc:"ESPHome proxy as host or host:port (default port 6053). Empty uses the stored host."`
		Key  string `json:"key,omitempty" doc:"ESPHome API encryption key, base64 of 32 bytes. Empty uses the stored key."`
	}
}

type testBLEIn struct {
	Body struct {
		Transport string `json:"transport,omitempty" enum:"local,esphome" doc:"local (this host's adapter) or esphome (a Bluetooth proxy). Empty uses the stored setting."`
		Address   string `json:"address,omitempty" doc:"Bike Bluetooth address to look for, such as AA:BB:CC:DD:EE:FF. Empty uses the stored address; with none, only the adapter is checked."`
		Host      string `json:"host,omitempty" doc:"ESPHome proxy as host or host:port. Empty uses the stored host."`
		Key       string `json:"key,omitempty" doc:"ESPHome API encryption key, base64 of 32 bytes. Empty uses the stored key."`
	}
}

type testOut struct {
	Body TestResult
}

func result(start time.Time, ok bool, format string, a ...any) *testOut {
	return &testOut{Body: TestResult{OK: ok, Message: fmt.Sprintf(format, a...),
		LatencyMS: int(time.Since(start).Milliseconds())}}
}

// sameURL reports whether a and b name the same base URL, ignoring the
// case of scheme and host and a trailing slash.
func sameURL(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host) &&
		strings.TrimRight(ua.Path, "/") == strings.TrimRight(ub.Path, "/") && ua.RawQuery == ub.RawQuery
}

func orStored(v, stored string) string {
	if v != "" {
		return v
	}
	return stored
}

func (d Deps) registerConnTests(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/ha", ID: "testHomeAssistant", Tag: "settings",
		Summary: "Test the Home Assistant connection",
		Description: "Calls `GET {url}/api/` with the token as a bearer credential, timing out after 5 seconds. " +
			"Empty body fields fall back to the stored settings. The result is always HTTP 200; check `ok`. " +
			"A 401 from Home Assistant reports `token rejected (401)`.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *testHAIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			base := orStored(in.Body.URL, s.HA.URL)
			token := in.Body.Token
			if token == "" {
				// The stored token goes only to the stored URL, never to a URL from the body.
				if in.Body.URL != "" && !sameURL(in.Body.URL, s.HA.URL) {
					return result(start, false, "token required when testing a different URL"), nil
				}
				token = s.HA.Token
			}
			u, err := url.Parse(base)
			if base == "" || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return result(start, false, "Home Assistant URL is missing or not an absolute http(s) URL"), nil
			}
			if token == "" {
				return result(start, false, "no Home Assistant token configured"), nil
			}
			ctx, cancel := context.WithTimeout(ctx, connTestTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/", nil)
			if err != nil {
				return result(start, false, "invalid Home Assistant URL"), nil
			}
			req.Header.Set("Authorization", "Bearer "+token)
			client := d.HTTPClient
			if client == nil {
				client = http.DefaultClient
			}
			resp, err := client.Do(req)
			if err != nil {
				msg := err.Error()
				if len(token) >= 8 { // a short token would mangle unrelated text
					msg = strings.ReplaceAll(msg, token, "[redacted]")
				}
				return result(start, false, "%s", msg), nil
			}
			_ = resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK:
				return result(start, true, "Home Assistant API reachable"), nil
			case resp.StatusCode == http.StatusUnauthorized:
				return result(start, false, "token rejected (401)"), nil
			default:
				return result(start, false, "unexpected response from Home Assistant: HTTP %d", resp.StatusCode), nil
			}
		})

	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/esphome", ID: "testESPHome", Tag: "settings",
		Summary: "Test the ESPHome Bluetooth proxy",
		Description: "Connects to the ESPHome device, performs the encrypted handshake and reads its device info, " +
			"then disconnects, timing out after 5 seconds. It never subscribes to Bluetooth and never talks to the " +
			"bike, so it is safe during a ride. Empty body fields fall back to the stored settings. The result is " +
			"always HTTP 200; check `ok`. A device without the Bluetooth proxy component reports `no bluetooth_proxy`.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *testESPHomeIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			host := orStored(in.Body.Host, s.Bike.ESPHomeHost)
			key := orStored(in.Body.Key, s.Bike.ESPHomeKey)
			if host == "" {
				return result(start, false, "no ESPHome host configured"), nil
			}
			if key == "" {
				return result(start, false, "no ESPHome key configured"), nil
			}
			if _, _, err := net.SplitHostPort(host); err != nil {
				host = net.JoinHostPort(strings.Trim(host, "[]"), esphomeDefaultPort)
			}
			ctx, cancel := context.WithTimeout(ctx, connTestTimeout)
			defer cancel()
			c, err := esphomeapi.Dial(ctx, host, key)
			if err != nil {
				msg := err.Error()
				if errors.Is(err, esphomeapi.ErrBadKey) {
					msg = "the device rejected the encryption key"
				}
				return result(start, false, "%s", msg), nil
			}
			defer c.Close()
			info := c.Info()
			if info.BTProxyFlags == 0 {
				return result(start, false, "%s has no bluetooth_proxy", info.Name), nil
			}
			return result(start, true, "connected to %s (ESPHome %s)", info.Name, info.ESPHomeVersion), nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/ble", ID: "testBLE", Tag: "settings",
		Summary: "Test the Bluetooth adapter and bike",
		Description: "Scans for 5 seconds with the local adapter or the ESPHome proxy and looks for the bike's address. " +
			"Without an address it only reports how many devices were seen. Empty body fields fall back to the stored settings. " +
			"The result is always HTTP 200; check `ok`. Refused with 409 `bike_busy` while a bike is connected, because one adapter cannot scan while holding a bike.",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *testBLEIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			addr := orStored(in.Body.Address, s.Bike.Address)
			var want blue.Address
			if addr != "" {
				if want, err = blue.ParseAddress(addr); err != nil {
					return result(start, false, "invalid bike address: %s", err), nil
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 3*connTestTimeout)
			defer cancel()
			devs, err := d.Bike.Scan(ctx, ridectl.ScanRequest{Transport: in.Body.Transport, Host: in.Body.Host,
				Key: in.Body.Key, DurationS: int(connTestTimeout / time.Second)})
			if err != nil {
				if errors.Is(err, ridectl.ErrBusy) || errors.Is(err, ridectl.ErrClosed) {
					return nil, bikeFailure(err)
				}
				return result(start, false, "%s. %s", err, ridectl.Hint(err)), nil
			}
			if addr == "" {
				return result(start, true, "adapter ok, %d devices seen", len(devs)), nil
			}
			for _, dev := range devs {
				if sameMAC(dev.Address, want) {
					proto := dev.Protocol
					if proto == "" {
						proto = "unknown"
					}
					return result(start, true, "found %s (%s) RSSI %d dBm, protocol %s", dev.Address, dev.Name, dev.RSSI, proto), nil
				}
			}
			return result(start, false, "not found: %s", ridectl.Hint(blue.ErrNotFound)), nil
		})
}

// sameMAC reports whether the scanned address (with an optional /random
// suffix) has the 48-bit address want.
func sameMAC(scanned string, want blue.Address) bool {
	mac, _, _ := strings.Cut(scanned, "/")
	a, err := blue.ParseAddress(mac)
	return err == nil && a.MAC == want.MAC
}
