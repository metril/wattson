package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/esphomeapi"
	"github.com/metril/wattson/internal/mapillary"
	"github.com/metril/wattson/internal/maptiles"
	"github.com/metril/wattson/internal/ridectl"
	wroute "github.com/metril/wattson/internal/route"
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

type testBRouterIn struct {
	Body struct {
		URL string `json:"url,omitempty" doc:"BRouter base URL to test, such as http://brouter:17777. Empty uses the stored URL."`
	}
}

type testMapillaryIn struct {
	Body struct {
		Token string `json:"token,omitempty" doc:"Mapillary access token to test. Empty uses the stored token."`
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
	d.registerBRouterTest(api)
	d.registerMapillaryTest(api)
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
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *testBLEIn) (*testOut, error) {
			start := time.Now()
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
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
			devs, err := m.Scan(ctx, ridectl.ScanRequest{Transport: in.Body.Transport, Host: in.Body.Host,
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

// brouterTestTimeout bounds the BRouter test; the first route after a start
// loads a segment file, which takes a few seconds.
const brouterTestTimeout = 10 * time.Second

// metresPerDegree is the length of one degree of latitude.
const metresPerDegree = 111320.0

func (d Deps) registerBRouterTest(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/brouter", ID: "testBRouter", Tag: "settings",
		Summary: "Test the BRouter connection",
		Description: "With at least one map region downloaded, asks BRouter for a 500 m route at the centre of that region and " +
			"reports the routed length. Without any, only checks that something answers HTTP at `{url}/brouter`. " +
			"Times out after 10 seconds, does not follow redirects and reports no reply text from the server, only status codes. " +
			"An empty `url` uses the stored one. The result is always HTTP 200; check `ok`.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *testBRouterIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			base := orStored(in.Body.URL, s.BRouter.URL)
			u, err := url.Parse(base)
			if base == "" || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return result(start, false, "BRouter URL is missing or not an absolute http(s) URL"), nil
			}
			ctx, cancel := context.WithTimeout(ctx, brouterTestTimeout)
			defer cancel()
			client := d.brouterClient()

			tile := ""
			if d.Tiles != nil {
				if _, ts, err := d.Tiles.List(ctx); err == nil {
					for _, t := range ts {
						if t.Status == maptiles.StatusPresent {
							tile = t.Name
							break
						}
					}
				}
			}
			if tile == "" {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/brouter", nil)
				if err != nil {
					return result(start, false, "invalid BRouter URL"), nil
				}
				resp, err := client.Do(req)
				if err != nil {
					return result(start, false, "BRouter is not reachable at %s", serverLabel(base)), nil
				}
				_ = resp.Body.Close()
				return result(start, true, "BRouter reachable (no map regions downloaded)"), nil
			}

			lat0, lon0, err := wroute.ParseTile(tile)
			if err != nil {
				return nil, err
			}
			lat, lon := float64(lat0)+2.5, float64(lon0)+2.5
			wps := []wroute.LonLat{{lon, lat}, {lon, lat + 500/metresPerDegree}}
			pts, err := wroute.BRouter{BaseURL: base, Client: client}.Route(ctx, wps, s.BRouter.Profile)
			if err != nil {
				var be *wroute.BRouterError
				switch {
				case errors.As(err, &be):
					return result(start, false, "BRouter answered HTTP %d for a test route in %s", be.Status, tile), nil
				case isTransport(err):
					return result(start, false, "BRouter is not reachable at %s", serverLabel(base)), nil
				}
				return result(start, false, "BRouter returned a reply that could not be used"), nil
			}
			var length float64
			for i := 1; i < len(pts); i++ {
				length += distanceM(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
			}
			return result(start, true, "BRouter routed %.0f m in %s", length, tile), nil
		})
}

// distanceM is the great-circle distance between two points in metres.
func distanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

func (d Deps) registerMapillaryTest(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/mapillary", ID: "testMapillary", Tag: "settings",
		Summary: "Test the Mapillary token",
		Description: "Makes one small Graph API request with the token as an OAuth credential in a header (never in a URL), " +
			"timing out after 5 seconds. An empty `token` uses the stored one. The result is always HTTP 200; check `ok`. " +
			"A 401 or 403 reports `token rejected (401)`. The token is never echoed back.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *testMapillaryIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			token := orStored(in.Body.Token, s.Mapillary.Token)
			if token == "" {
				return result(start, false, "no Mapillary token saved"), nil
			}
			ctx, cancel := context.WithTimeout(ctx, connTestTimeout)
			defer cancel()
			c := &mapillary.Client{GraphURL: s.Mapillary.GraphURL, TilesURL: s.Mapillary.TilesURL, HTTP: d.HTTPClient,
				Token: func(context.Context) (string, error) { return token, nil }}
			switch err := c.Ping(ctx); {
			case err == nil:
				return result(start, true, "Mapillary token accepted"), nil
			case errors.Is(err, mapillary.ErrTokenRejected):
				return result(start, false, "%s", err), nil // "token rejected (401)"
			case errors.Is(err, mapillary.ErrRateLimited):
				return result(start, false, "Mapillary is rate limiting requests, try again shortly"), nil
			case ctx.Err() != nil:
				return result(start, false, "Mapillary did not answer in time"), nil
			default:
				return result(start, false, "Mapillary is not reachable or answered unexpectedly"), nil
			}
		})
}
