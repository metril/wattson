package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/ridectl"
)

// riderOf returns the first rider ID of the client's user.
func (ts *testServer) riderOf(t testing.TB, c *http.Client) int64 {
	t.Helper()
	resp, b := ts.do(t, c, "GET", "/api/riders", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("riders: %d %s", resp.StatusCode, b)
	}
	rs := decode[[]RiderOut](t, b)
	if len(rs) == 0 {
		t.Fatal("no riders")
	}
	return rs[0].ID
}

// connectSim connects the client's rider to the simulator and waits for ready.
func (ts *testServer) connectSim(t testing.TB, c *http.Client) int64 {
	t.Helper()
	rid := ts.riderOf(t, c)
	resp, b := ts.do(t, c, "POST", "/api/bike/connect", map[string]any{"rider_id": rid})
	if resp.StatusCode != 202 {
		t.Fatalf("connect: %d %s", resp.StatusCode, b)
	}
	ts.waitBike(t, c, "ready")
	return rid
}

func poll(t testing.TB, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func (ts *testServer) bikeStatus(t testing.TB, c *http.Client) ridectl.Status {
	t.Helper()
	resp, b := ts.do(t, c, "GET", "/api/bike", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/bike: %d %s", resp.StatusCode, b)
	}
	return decode[ridectl.Status](t, b)
}

func (ts *testServer) waitBike(t testing.TB, c *http.Client, state string) ridectl.Status {
	t.Helper()
	var st ridectl.Status
	poll(t, "bike state "+state, func() bool { st = ts.bikeStatus(t, c); return st.State == state })
	return st
}

func (ts *testServer) ride(t testing.TB, c *http.Client) engine.Tick {
	t.Helper()
	resp, b := ts.do(t, c, "GET", "/api/ride", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/ride: %d %s", resp.StatusCode, b)
	}
	return decode[engine.Tick](t, b)
}

// syntheticRoute creates a 900 m route (300 m flat, 600 m at 6 %) and returns its id.
func (ts *testServer) syntheticRoute(t testing.TB, c *http.Client) int64 {
	t.Helper()
	resp, b := ts.do(t, c, "POST", "/api/routes/synthetic", map[string]any{"name": "ramp",
		"segments": []map[string]any{{"length_m": 300, "grade_pct": 0}, {"length_m": 600, "grade_pct": 6}}})
	if resp.StatusCode != 201 {
		t.Fatalf("synthetic: %d %s", resp.StatusCode, b)
	}
	return decode[RouteDetail](t, b).ID
}

func (ts *testServer) command(t testing.TB, c *http.Client, cmd string, value int) (*http.Response, []byte) {
	t.Helper()
	body := map[string]any{"command": cmd}
	if value != 0 {
		body["value"] = value
	}
	return ts.do(t, c, "POST", "/api/ride/command", body)
}

func TestBikeConnectSim(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	rid := ts.riderOf(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/bike/connect", map[string]any{"rider_id": rid})
	if resp.StatusCode != 202 {
		t.Fatalf("connect: %d %s", resp.StatusCode, b)
	}
	if st := decode[ridectl.Status](t, b); st.State != "connecting" || st.RiderID != rid {
		t.Errorf("connect status = %+v", st)
	}
	st := ts.waitBike(t, admin, "ready")
	if st.Protocol != "sim" || !st.Connected {
		t.Errorf("ready status = %+v", st)
	}
}

func TestRideCommandFlow(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)

	if resp, b := ts.command(t, admin, "start", 0); resp.StatusCode != 204 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	poll(t, "riding", func() bool { return ts.ride(t, admin).State == "riding" })
	if resp, b := ts.command(t, admin, "set_level", 9); resp.StatusCode != 204 {
		t.Fatalf("set_level: %d %s", resp.StatusCode, b)
	}
	poll(t, "level 9", func() bool { return ts.ride(t, admin).Level == 9 })
	if resp, b := ts.command(t, admin, "stop", 0); resp.StatusCode != 204 {
		t.Fatalf("stop: %d %s", resp.StatusCode, b)
	}
	ts.waitBike(t, admin, "ready")
}

func TestRideCommandNoBike(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.command(t, admin, "start", 0)
	if resp.StatusCode != 409 || errCode(t, b) != "no_bike" {
		t.Errorf("got %d %s, want 409 no_bike", resp.StatusCode, b)
	}
}

func TestBadState(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	resp, b := ts.command(t, admin, "resume", 0)
	if resp.StatusCode != 409 || errCode(t, b) != "bad_state" {
		t.Errorf("got %d %s, want 409 bad_state", resp.StatusCode, b)
	}
}

func TestUnknownCommandRejected(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.command(t, admin, "boost", 0)
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" {
		t.Errorf("got %d %s, want 400 invalid_request", resp.StatusCode, b)
	}
}

func TestDisconnectWhileRiding(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	if resp, b := ts.command(t, admin, "start", 0); resp.StatusCode != 204 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	poll(t, "riding", func() bool { return ts.ride(t, admin).State == "riding" })

	resp, b := ts.do(t, admin, "POST", "/api/bike/disconnect", nil)
	if resp.StatusCode != 409 || errCode(t, b) != "ride_active" {
		t.Errorf("disconnect: %d %s, want 409 ride_active", resp.StatusCode, b)
	}
	for i := 0; i < 2; i++ {
		if resp, b := ts.do(t, admin, "POST", "/api/bike/release", nil); resp.StatusCode != 204 {
			t.Fatalf("release #%d: %d %s", i+1, resp.StatusCode, b)
		}
	}
	if st := ts.bikeStatus(t, admin); st.State != "idle" {
		t.Errorf("state after release = %q, want idle", st.State)
	}
	if resp, b := ts.do(t, admin, "POST", "/api/bike/disconnect", nil); resp.StatusCode != 204 {
		t.Errorf("disconnect when idle: %d %s", resp.StatusCode, b)
	}
}

func TestConnectOtherUsersRider(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	bobRider := ts.riderOf(t, bob)
	resp, b := ts.do(t, admin, "POST", "/api/bike/connect", map[string]any{"rider_id": bobRider})
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("got %d %s, want 404", resp.StatusCode, b)
	}
}

func TestConnectTwice(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	rid := ts.connectSim(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/bike/connect", map[string]any{"rider_id": rid})
	if resp.StatusCode != 409 || errCode(t, b) != "bike_busy" {
		t.Errorf("got %d %s, want 409 bike_busy", resp.StatusCode, b)
	}
}

func TestConnectNoBikeAddress(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"bike": map[string]any{"driver": "auto"}}); resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	rid := ts.riderOf(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/bike/connect", map[string]any{"rider_id": rid})
	if resp.StatusCode != 400 || errCode(t, b) != "no_bike_address" {
		t.Errorf("got %d %s, want 400 no_bike_address", resp.StatusCode, b)
	}
}

func TestReadTokenCannotCommand(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	read := ts.mintToken(t, "reader", false, "read")
	resp, b := ts.do(t, nil, "POST", "/api/ride/command", map[string]any{"command": "start"}, bearer(read)...)
	if resp.StatusCode != 403 {
		t.Errorf("command: %d %s, want 403", resp.StatusCode, b)
	}
	resp, b = ts.do(t, nil, "POST", "/api/bike/disconnect", nil, bearer(read)...)
	if resp.StatusCode != 403 {
		t.Errorf("disconnect: %d %s, want 403", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, nil, "GET", "/api/bike", nil, bearer(read)...); resp.StatusCode != 200 {
		t.Errorf("read GET /api/bike: %d %s, want 200", resp.StatusCode, b)
	}
}

func TestDeleteRiderInUse(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	// A second rider so the last-rider rule cannot be what refuses.
	resp, b := ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "Second"})
	if resp.StatusCode != 201 {
		t.Fatalf("create rider: %d %s", resp.StatusCode, b)
	}
	rid := ts.connectSim(t, admin)
	resp, b = ts.do(t, admin, "DELETE", "/api/riders/"+itoa(rid), nil)
	if resp.StatusCode != 409 || errCode(t, b) != "rider_in_use" {
		t.Errorf("got %d %s, want 409 rider_in_use", resp.StatusCode, b)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// scanFake is a scan-only transport that reports advs and returns at once.
type scanFake struct{ advs []blue.Advertisement }

func (f *scanFake) Scan(_ context.Context, fn func(blue.Advertisement)) error {
	for _, a := range f.advs {
		fn(a)
	}
	return nil
}

func (f *scanFake) Connect(context.Context, blue.Address) (blue.Conn, error) {
	return nil, blue.ErrNotFound
}

func (f *scanFake) Close() error { return nil }

var fakeBikeAddr = blue.Address{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}}

func fakeBike() *scanFake {
	return &scanFake{advs: []blue.Advertisement{{Addr: fakeBikeAddr, Name: "MRK-S29R2", RSSI: -55,
		Services: []blue.UUID{bike.FitPlusService}}}}
}

func TestBLETest(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOpenLocal(func() (blue.Transport, error) { return fakeBike(), nil }))
	admin := ts.setupAdmin(t)
	post := func(body map[string]any) TestResult {
		t.Helper()
		resp, b := ts.do(t, admin, "POST", "/api/settings/test/ble", body)
		if resp.StatusCode != 200 {
			t.Fatalf("test/ble: %d %s", resp.StatusCode, b)
		}
		return decode[TestResult](t, b)
	}
	if r := post(map[string]any{"address": "AA:BB:CC:DD:EE:FF"}); !r.OK ||
		!strings.HasPrefix(r.Message, "found AA:BB:CC:DD:EE:FF (MRK-S29R2)") || !strings.Contains(r.Message, "protocol fitplus") {
		t.Errorf("found = %+v", r)
	}
	if r := post(map[string]any{"address": "11:22:33:44:55:66"}); r.OK || !strings.HasPrefix(r.Message, "not found: Bike not found.") {
		t.Errorf("not found = %+v", r)
	}
	if r := post(map[string]any{}); !r.OK || r.Message != "adapter ok, 1 devices seen" {
		t.Errorf("no address = %+v", r)
	}
	if r := post(map[string]any{"address": "nonsense"}); r.OK {
		t.Errorf("bad address = %+v", r)
	}
}

func TestBLETestTransportError(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOpenLocal(func() (blue.Transport, error) { return nil, blue.ErrNotFound }))
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/settings/test/ble", map[string]any{})
	r := decode[TestResult](t, b)
	if resp.StatusCode != 200 || r.OK || !strings.Contains(r.Message, "blue: device not found") || !strings.Contains(r.Message, "Bike not found.") {
		t.Errorf("got %d %+v", resp.StatusCode, r)
	}
}

func TestBLETestBusy(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/settings/test/ble", map[string]any{})
	if resp.StatusCode != 409 || errCode(t, b) != "bike_busy" {
		t.Errorf("got %d %s, want 409 bike_busy", resp.StatusCode, b)
	}
}

func TestScanEndpoint(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOpenLocal(func() (blue.Transport, error) { return fakeBike(), nil }))
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/bike/scan", map[string]any{"duration_s": 1})
	if resp.StatusCode != 200 {
		t.Fatalf("scan: %d %s", resp.StatusCode, b)
	}
	out := decode[ScanOut](t, b)
	if len(out.Devices) != 1 || out.Devices[0].Address != "AA:BB:CC:DD:EE:FF" || out.Devices[0].Protocol != "fitplus" {
		t.Errorf("devices = %+v", out.Devices)
	}
	if resp, b := ts.do(t, admin, "POST", "/api/bike/scan", map[string]any{"duration_s": 99}); resp.StatusCode != 400 {
		t.Errorf("duration 99: %d %s, want 400", resp.StatusCode, b)
	}
	ts.connectSim(t, admin)
	resp, b = ts.do(t, admin, "POST", "/api/bike/scan", map[string]any{})
	if resp.StatusCode != 409 || errCode(t, b) != "bike_busy" {
		t.Errorf("scan while connected: %d %s, want 409 bike_busy", resp.StatusCode, b)
	}
}

func TestScanRequiresAdmin(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ctl := ts.mintToken(t, "driver", false, "control")
	resp, b := ts.do(t, nil, "POST", "/api/bike/scan", map[string]any{}, bearer(ctl)...)
	if resp.StatusCode != 403 {
		t.Errorf("got %d %s, want 403", resp.StatusCode, b)
	}
}

func TestNoBikeManager503(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withoutBike())
	admin := ts.setupAdmin(t)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/bike", nil},
		{"POST", "/api/bike/connect", map[string]any{"rider_id": 1}},
		{"POST", "/api/bike/disconnect", nil},
		{"POST", "/api/bike/release", nil},
		{"POST", "/api/bike/scan", map[string]any{}},
		{"POST", "/api/settings/test/ble", map[string]any{}},
		{"GET", "/api/ride", nil},
		{"POST", "/api/ride/command", map[string]any{"command": "start"}},
	} {
		resp, b := ts.do(t, admin, c.method, c.path, c.body)
		if resp.StatusCode != 503 || errCode(t, b) != "no_bike_manager" {
			t.Errorf("%s %s: %d %s, want 503 no_bike_manager", c.method, c.path, resp.StatusCode, b)
		}
	}
}

// dropRec is a simulator dial that records sessions and can make the bike
// unreachable, so a release cannot confirm the floor level.
type dropRec struct {
	mu   sync.Mutex
	sess []*sim.Session
	fail atomic.Bool
}

func (r *dropRec) dial(c sim.Config) bike.Dial {
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

func (r *dropRec) first() *sim.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sess) == 0 {
		return nil
	}
	return r.sess[0]
}

// loseBikeMidRide rides, then drops the link with the bike unreachable.
func loseBikeMidRide(t *testing.T, ts *testServer, admin *http.Client, rec *dropRec) {
	t.Helper()
	ts.connectSim(t, admin)
	if resp, b := ts.command(t, admin, "start", 0); resp.StatusCode != 204 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	rec.fail.Store(true)
	rec.first().Drop()
	poll(t, "link down", func() bool { return !ts.bikeStatus(t, admin).Connected })
}

func TestReleaseFloorUnconfirmed424(t *testing.T) {
	t.Parallel()
	rec := &dropRec{}
	ts := newTS(t, withSimDial(rec.dial))
	admin := ts.setupAdmin(t)
	loseBikeMidRide(t, ts, admin, rec)
	resp, b := ts.do(t, admin, "POST", "/api/bike/release", nil)
	if resp.StatusCode != 424 || errCode(t, b) != "floor_unconfirmed" {
		t.Fatalf("release: %d %s, want 424 floor_unconfirmed", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), "lower the resistance at the console") {
		t.Errorf("message lacks advice: %s", b)
	}
	if st := ts.bikeStatus(t, admin); st.State != "idle" || !strings.Contains(st.LastError, "lower resistance") || !st.FloorUnconfirmed {
		t.Errorf("status after release = %+v", st)
	}
}

func TestDisconnectFloorUnconfirmed424(t *testing.T) {
	t.Parallel()
	rec := &dropRec{}
	ts := newTS(t, withSimDial(rec.dial))
	admin := ts.setupAdmin(t)
	loseBikeMidRide(t, ts, admin, rec)
	if resp, b := ts.command(t, admin, "stop", 0); resp.StatusCode != 204 {
		t.Fatalf("stop: %d %s", resp.StatusCode, b)
	}
	resp, b := ts.do(t, admin, "POST", "/api/bike/disconnect", nil)
	if resp.StatusCode != 424 || errCode(t, b) != "floor_unconfirmed" {
		t.Errorf("disconnect: %d %s, want 424 floor_unconfirmed", resp.StatusCode, b)
	}
}

func TestScanFailed424(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOpenLocal(func() (blue.Transport, error) { return nil, blue.ErrNotFound }))
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/bike/scan", map[string]any{"duration_s": 1})
	if resp.StatusCode != 424 || errCode(t, b) != "scan_failed" {
		t.Errorf("scan: %d %s, want 424 scan_failed", resp.StatusCode, b)
	}
}

func TestRideStartRoute(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	id := ts.syntheticRoute(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/ride/command", map[string]any{"command": "start", "route_id": id})
	if resp.StatusCode != 204 {
		t.Fatalf("start route: %d %s", resp.StatusCode, b)
	}
	poll(t, "route ride", func() bool {
		tk := ts.ride(t, admin)
		return tk.State == "riding" && tk.RouteID == id && tk.Mode == "route" && tk.RouteLengthM > 0
	})
}

func TestRouteIDWithoutStart(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	id := ts.syntheticRoute(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/ride/command", map[string]any{"command": "pause", "route_id": id})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" {
		t.Fatalf("pause+route_id: %d %s", resp.StatusCode, b)
	}
}

func TestStartUnknownRoute(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	resp, b := ts.do(t, admin, "POST", "/api/ride/command", map[string]any{"command": "start", "route_id": 999})
	if resp.StatusCode != 404 || errCode(t, b) != "route_not_found" {
		t.Fatalf("unknown route: %d %s", resp.StatusCode, b)
	}
}
