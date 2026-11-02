package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/ridectl"
)

// wsDial opens /ws on ts offering the Wattson subprotocol plus extra, with
// the cookies c holds. It returns the connection or the failed handshake's
// response.
func (ts *testServer) wsDial(t testing.TB, c *http.Client, hdr http.Header, extra ...string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	if hdr == nil {
		hdr = http.Header{}
	}
	if c != nil {
		pu, _ := url.Parse(ts.URL)
		for _, ck := range c.Jar.Cookies(pu) {
			hdr.Add("Cookie", ck.Name+"="+ck.Value)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: hdr, Subprotocols: append([]string{auth.WSProtocol}, extra...)})
	if err == nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func (ts *testServer) mustWS(t testing.TB, c *http.Client, hdr http.Header, extra ...string) *websocket.Conn {
	t.Helper()
	conn, resp, err := ts.wsDial(t, c, hdr, extra...)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("ws dial: %v (HTTP %d)", err, code)
	}
	return conn
}

type wsMsg map[string]any

func (m wsMsg) str(k string) string { s, _ := m[k].(string); return s }

func (m wsMsg) sub(k string) wsMsg { s, _ := m[k].(map[string]any); return wsMsg(s) }

func wsRead(t testing.TB, conn *websocket.Conn) wsMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var m wsMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("ws message %q: %v", b, err)
	}
	return m
}

// wsReadUntil reads until pred holds for a message.
func wsReadUntil(t testing.TB, conn *websocket.Conn, what string, pred func(wsMsg) bool) wsMsg {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		if m := wsRead(t, conn); pred(m) {
			return m
		}
	}
	t.Fatalf("no message: %s", what)
	return nil
}

func wsSend(t testing.TB, conn *websocket.Conn, raw string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

func wsCmd(t testing.TB, conn *websocket.Conn, id, command string, value int) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "command", "id": id, "command": command, "value": value})
	wsSend(t, conn, string(b))
}

func TestWSStateOnConnect(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	conn := ts.mustWS(t, admin, nil)
	m := wsRead(t, conn)
	if m.str("type") != "state" || m.sub("bike").str("state") != "idle" || m.sub("tick").str("state") != "idle" {
		t.Errorf("first message = %v", m)
	}
	if conn.Subprotocol() != auth.WSProtocol {
		t.Errorf("subprotocol = %q", conn.Subprotocol())
	}
}

func TestWSRequiresAuth(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	_, resp, err := ts.wsDial(t, nil, nil)
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("anonymous: err=%v resp=%v, want HTTP 401", err, resp)
	}
}

func TestWSRequiresSubprotocol(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	read := ts.mintToken(t, "reader", false, "read")
	hdr := http.Header{"Authorization": {"Bearer " + read}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil || resp == nil || resp.StatusCode != 400 {
		t.Errorf("no subprotocol: err=%v resp=%v, want HTTP 400", err, resp)
	}
}

func TestWSForeignOrigin403(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, resp, err := ts.wsDial(t, admin, http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Errorf("foreign origin: err=%v resp=%v, want HTTP 403", err, resp)
	}
	// The server's own origin is fine.
	conn := ts.mustWS(t, admin, http.Header{"Origin": {ts.URL}})
	if m := wsRead(t, conn); m.str("type") != "state" {
		t.Errorf("same origin first message = %v", m)
	}
}

func TestWSPublicURLOrigin(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"general": map[string]any{"public_url": "https://wattson.example.com"}}); resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	conn := ts.mustWS(t, admin, http.Header{"Origin": {"https://wattson.example.com"}})
	if m := wsRead(t, conn); m.str("type") != "state" {
		t.Errorf("public origin first message = %v", m)
	}
}

func TestWSTokenSubprotocol(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	read := ts.mintToken(t, "reader", false, "read")
	conn := ts.mustWS(t, nil, nil, "bearer."+read)
	if conn.Subprotocol() != auth.WSProtocol {
		t.Errorf("subprotocol = %q, want %q", conn.Subprotocol(), auth.WSProtocol)
	}
	if m := wsRead(t, conn); m.str("type") != "state" {
		t.Errorf("first message = %v", m)
	}
}

func TestWSBadTokenIs401(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	_, resp, err := ts.wsDial(t, nil, nil, "bearer.wat_nope")
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Errorf("bad token: err=%v resp=%v, want HTTP 401", err, resp)
	}
}

func TestWSCommandRoundTrip(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	conn := ts.mustWS(t, admin, nil)
	if m := wsRead(t, conn); m.str("type") != "state" || m.sub("bike").str("state") != "ready" {
		t.Fatalf("first message = %v", m)
	}
	wsCmd(t, conn, "c1", "start", 0)
	ack := wsReadUntil(t, conn, "ack c1", func(m wsMsg) bool { return m.str("type") == "ack" })
	if ack.str("id") != "c1" {
		t.Errorf("ack = %v", ack)
	}
	wsReadUntil(t, conn, "riding tick", func(m wsMsg) bool {
		return m.str("type") == "tick" && m.sub("tick").str("state") == "riding"
	})
	wsCmd(t, conn, "c2", "level_delta", 1)
	wsReadUntil(t, conn, "level 6 tick", func(m wsMsg) bool {
		l, _ := m.sub("tick")["level"].(float64)
		return m.str("type") == "tick" && l == 6
	})
}

func TestWSBikeStatusChange(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	ts.connectSim(t, admin)
	wsReadUntil(t, conn, "bike ready", func(m wsMsg) bool {
		return m.str("type") == "bike" && m.sub("bike").str("state") == "ready"
	})
}

func TestWSReadTokenForbidden(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	read := ts.mintToken(t, "reader", false, "read")
	conn := ts.mustWS(t, nil, nil, "bearer."+read)
	wsRead(t, conn)
	wsCmd(t, conn, "c1", "start", 0)
	m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.str("id") != "c1" || m.sub("error").str("code") != "forbidden" {
		t.Errorf("error = %v", m)
	}
}

func TestWSErrorsNoBikeAndBadState(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsCmd(t, conn, "n1", "start", 0)
	m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.str("id") != "n1" || m.sub("error").str("code") != "no_bike" {
		t.Errorf("no bike = %v", m)
	}
	ts.connectSim(t, admin)
	wsCmd(t, conn, "n2", "resume", 0)
	m = wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.str("id") != "n2" || m.sub("error").str("code") != "bad_state" {
		t.Errorf("bad state = %v", m)
	}
}

func TestWSUnknownCommand(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsCmd(t, conn, "boost", "boost", 200)
	m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.str("id") != "boost" || m.sub("error").str("code") != "unknown_command" {
		t.Errorf("boost: %v", m)
	}
	// set_target_power is a known command, but a free ride has no target
	wsCmd(t, conn, "stp", "set_target_power", 200)
	m = wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.str("id") != "stp" || m.sub("error").str("code") != "bad_state" {
		t.Errorf("set_target_power in free ride: %v", m)
	}
}

func TestWSSetTargetPower(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsSend(t, conn, `{"type":"command","id":"e0","command":"start","target_power_w":200}`)
	if m := wsReadUntil(t, conn, "ack", func(m wsMsg) bool { return m.str("type") == "ack" || m.str("type") == "error" }); m.str("type") != "ack" || m.str("id") != "e0" {
		t.Fatalf("start erg = %v", m)
	}
	wsCmd(t, conn, "e1", "set_target_power", 260)
	if m := wsReadUntil(t, conn, "ack", func(m wsMsg) bool { return m.str("type") == "ack" || m.str("type") == "error" }); m.str("type") != "ack" || m.str("id") != "e1" {
		t.Fatalf("set_target_power = %v", m)
	}
	wsReadUntil(t, conn, "tick with target 260", func(m wsMsg) bool {
		w, _ := m.sub("tick")["target_power_w"].(float64)
		return m.str("type") == "tick" && w == 260
	})
	wsCmd(t, conn, "e2", "set_target_power", 20)
	if m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" }); m.str("id") != "e2" || m.sub("error").str("code") != "invalid_value" {
		t.Errorf("set_target_power 20 = %v", m)
	}
}

func TestWSStartTargetsValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	for id, raw := range map[string]string{
		"two":   `{"type":"command","id":"two","command":"start","workout_id":1,"target_power_w":200}`,
		"wk":    `{"type":"command","id":"wk","command":"pause","workout_id":1}`,
		"tp":    `{"type":"command","id":"tp","command":"pause","target_power_w":200}`,
		"three": `{"type":"command","id":"three","command":"start","route_id":1,"workout_id":1,"target_power_w":200}`,
	} {
		wsSend(t, conn, raw)
		m := wsReadUntil(t, conn, "error for "+id, func(m wsMsg) bool { return m.str("type") == "error" })
		if m.str("id") != id || m.sub("error").str("code") != "invalid_request" {
			t.Errorf("%s: %v", id, m)
		}
	}
	wsSend(t, conn, `{"type":"command","id":"nw","command":"start","workout_id":999}`)
	if m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" }); m.sub("error").str("code") != "workout_not_found" {
		t.Errorf("unknown workout = %v", m)
	}
}

func TestWSMalformed(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsSend(t, conn, "{")
	m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.sub("error").str("code") != "invalid_request" {
		t.Errorf("malformed = %v", m)
	}
	wsSend(t, conn, `{"type":"ping"}`)
	m = wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" })
	if m.sub("error").str("code") != "invalid_request" {
		t.Errorf("wrong type = %v", m)
	}
	wsCmd(t, conn, "c1", "start", 0)
	m = wsReadUntil(t, conn, "ack", func(m wsMsg) bool { return m.str("type") == "ack" || m.str("type") == "error" })
	if m.str("type") != "ack" || m.str("id") != "c1" {
		t.Errorf("after malformed = %v", m)
	}
}

func TestWSOversizedClosed(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsSend(t, conn, `{"type":"command","id":"`+strings.Repeat("x", 5000)+`"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
				t.Errorf("close status = %d (%v), want 1009", websocket.CloseStatus(err), err)
			}
			return
		}
	}
}

func TestWSRevokedTokenClosed(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withWSRecheck(50*time.Millisecond))
	tok := ts.mintToken(t, "driver", false, "read", "control")
	conn := ts.mustWS(t, nil, nil, "bearer."+tok)
	wsRead(t, conn)
	toks, err := ts.Store.ListAPITokens(context.Background(), 1)
	if err != nil || len(toks) != 1 {
		t.Fatalf("tokens: %v %v", toks, err)
	}
	if err := ts.Store.DeleteAPIToken(context.Background(), 1, toks[0].ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != 4401 {
				t.Errorf("close status = %d (%v), want 4401", websocket.CloseStatus(err), err)
			}
			return
		}
	}
}

func TestWSSlowClientDoesNotStall(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	// A subscriber that never reads: with a buffer of 2 it must lose its
	// oldest events while the publisher (and everyone else) carries on.
	blocked, unsub := ts.Bike.Subscribe(2)
	defer unsub()
	slow := ts.mustWS(t, admin, nil) // a WebSocket client that never reads
	_ = slow
	fast := ts.mustWS(t, admin, nil)
	if resp, b := ts.command(t, admin, "start", 0); resp.StatusCode != 204 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	ticks := 0
	var lastElapsed float64
	for end := time.Now().Add(2 * time.Second); ticks < 15 && time.Now().Before(end); {
		if m := wsRead(t, fast); m.str("type") == "tick" {
			ticks++
			lastElapsed, _ = m.sub("tick")["elapsed_s"].(float64)
		}
	}
	if ticks < 15 {
		t.Fatalf("fast client saw %d ticks in 2 s, want >= 15", ticks)
	}
	// Drain the blocked subscriber: it holds at most its buffer, the newest
	// events, not the first ones it was sent.
	var got []ridectl.Event
	for drained := false; !drained; {
		select {
		case ev, ok := <-blocked:
			if !ok {
				drained = true
				break
			}
			got = append(got, ev)
		default:
			drained = true
		}
	}
	if len(got) != 2 {
		t.Fatalf("blocked subscriber holds %d events, want its buffer of 2", len(got))
	}
	last := got[1]
	if last.Type != "tick" || last.Tick == nil || last.Tick.ElapsedS < lastElapsed-0.5 {
		t.Errorf("blocked subscriber's newest event = %+v, want a recent tick (fast client reached %.2f s)", last, lastElapsed)
	}
}

// wsMany opens n WebSockets with the bearer token and returns how many the
// server accepted and the status of the first refusal (0 if none).
func (ts *testServer) wsMany(t testing.TB, token string, n int) (accepted, refusal int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, resp, err := ts.wsDial(t, nil, nil, "bearer."+token)
		if err != nil {
			if resp == nil {
				t.Fatalf("dial %d: %v", i, err)
			}
			return accepted, resp.StatusCode
		}
		accepted++
	}
	return accepted, 0
}

func TestWSConnectionLimit(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	// 4 users x 8 connections fill the global limit of 32 without any one
	// user hitting the per-user cap.
	for u := 0; u < maxWSConns/maxWSPerUser; u++ {
		tok := ts.mintToken(t, "user"+strconv.Itoa(u), false, "read")
		if n, ref := ts.wsMany(t, tok, maxWSPerUser); n != maxWSPerUser || ref != 0 {
			t.Fatalf("user %d: accepted %d, refusal %d", u, n, ref)
		}
	}
	extra := ts.mintToken(t, "extra", false, "read")
	_, resp, err := ts.wsDial(t, nil, nil, "bearer."+extra)
	if err == nil || resp == nil || resp.StatusCode != 503 {
		t.Errorf("33rd connection: err=%v resp=%v, want HTTP 503", err, resp)
	}
}

func TestWSPerUserLimit(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "greedy", false, "read")
	n, ref := ts.wsMany(t, tok, maxWSPerUser+1)
	if n != maxWSPerUser || ref != 503 {
		t.Fatalf("accepted %d, refusal %d; want %d then 503", n, ref, maxWSPerUser)
	}
	// Another user is unaffected.
	other := ts.mintToken(t, "polite", false, "read")
	if n, ref := ts.wsMany(t, other, 1); n != 1 || ref != 0 {
		t.Errorf("other user: accepted %d, refusal %d", n, ref)
	}
}

func TestWSLimitSlotFreedOnClose(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "cycler", false, "read")
	conns := make([]*websocket.Conn, 0, maxWSPerUser)
	for i := 0; i < maxWSPerUser; i++ {
		conns = append(conns, ts.mustWS(t, nil, nil, "bearer."+tok))
	}
	_ = conns[0].Close(websocket.StatusNormalClosure, "bye")
	poll(t, "slot freed", func() bool {
		c, _, err := ts.wsDial(t, nil, nil, "bearer."+tok)
		return err == nil && c != nil
	})
}

func BenchmarkWSCommandAck(b *testing.B) {
	ts := newTS(b)
	admin := ts.setupAdmin(b)
	ts.connectSim(b, admin)
	conn := ts.mustWS(b, admin, nil)
	wsRead(b, conn)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wsCmd(b, conn, "b", "level_delta", 0)
		wsReadUntil(b, conn, "ack", func(m wsMsg) bool { return m.str("type") == "ack" })
	}
}

func TestWSStartRoute(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.connectSim(t, admin)
	id := ts.syntheticRoute(t, admin)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	wsSend(t, conn, fmt.Sprintf(`{"type":"command","id":"r0","command":"pause","route_id":%d}`, id))
	if m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" }); m.sub("error").str("code") != "invalid_request" {
		t.Errorf("route_id with pause = %v", m)
	}
	wsSend(t, conn, `{"type":"command","id":"r1","command":"start","route_id":999}`)
	if m := wsReadUntil(t, conn, "error", func(m wsMsg) bool { return m.str("type") == "error" }); m.str("id") != "r1" || m.sub("error").str("code") != "route_not_found" {
		t.Errorf("unknown route = %v", m)
	}
	wsSend(t, conn, fmt.Sprintf(`{"type":"command","id":"r2","command":"start","route_id":%d}`, id))
	if m := wsReadUntil(t, conn, "ack", func(m wsMsg) bool { return m.str("type") == "ack" }); m.str("id") != "r2" {
		t.Errorf("ack = %v", m)
	}
	wsReadUntil(t, conn, "route tick", func(m wsMsg) bool {
		rid, _ := m.sub("tick")["route_id"].(float64)
		return m.str("type") == "tick" && int64(rid) == id
	})
}
