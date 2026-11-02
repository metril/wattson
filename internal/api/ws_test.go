package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/metril/wattson/internal/auth"
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

func TestWSUnknownAndUnsupported(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	conn := ts.mustWS(t, admin, nil)
	wsRead(t, conn)
	for cmd, code := range map[string]string{"boost": "unknown_command", "set_target_power": "unsupported_command"} {
		wsCmd(t, conn, cmd, cmd, 200)
		m := wsReadUntil(t, conn, "error for "+cmd, func(m wsMsg) bool { return m.str("type") == "error" })
		if m.str("id") != cmd || m.sub("error").str("code") != code {
			t.Errorf("%s: %v, want code %s", cmd, m, code)
		}
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
	if resp, b := ts.command(t, admin, "start", 0); resp.StatusCode != 204 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	slow := ts.mustWS(t, admin, nil) // never reads
	_ = slow
	fast := ts.mustWS(t, admin, nil)
	ticks := 0
	for end := time.Now().Add(time.Second); ticks < 10 && time.Now().Before(end); {
		if wsRead(t, fast).str("type") == "tick" {
			ticks++
		}
	}
	if ticks < 10 {
		t.Errorf("fast client saw %d ticks in 1 s, want >= 10", ticks)
	}
}

func TestWSConnectionLimit(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for i := 0; i < maxWSConns; i++ {
		ts.mustWS(t, admin, nil)
	}
	_, resp, err := ts.wsDial(t, admin, nil)
	if err == nil || resp == nil || resp.StatusCode != 503 {
		t.Errorf("33rd connection: err=%v resp=%v, want HTTP 503", err, resp)
	}
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
