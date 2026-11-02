package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/engine"
)

const (
	maxWSConns     = 32
	maxWSPerUser   = 8
	wsReadLimit    = 4096
	wsWriteTimeout = 5 * time.Second
	// wsCommandTimeout bounds one command's Manager.Do.
	wsCommandTimeout = 5 * time.Second
	wsPingEvery      = 30 * time.Second
	wsDefaultCheck   = time.Minute
	wsEventBuf       = 64

	// wsUnauthorized closes a connection whose credential stopped being valid.
	wsUnauthorized = websocket.StatusCode(4401)
)

// wsGate counts open WebSocket connections across the server and per user.
type wsGate struct {
	mu    sync.Mutex
	total int
	users map[int64]int
}

// acquire reserves a slot for user uid; ok is false (with a reason for the
// client) when the server or the user is at its limit. Call release once.
func (g *wsGate) acquire(uid int64) (release func(), reason string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.total >= maxWSConns {
		return nil, "too many open WebSocket connections", false
	}
	if g.users[uid] >= maxWSPerUser {
		return nil, "too many open WebSocket connections for this user", false
	}
	if g.users == nil {
		g.users = map[int64]int{}
	}
	g.total++
	g.users[uid]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.total--
		if g.users[uid]--; g.users[uid] <= 0 {
			delete(g.users, uid)
		}
	}, "", true
}

func init() {
	registerSpec(func() []opSpec {
		return []opSpec{{
			Method: http.MethodGet, Path: "/ws", ID: "connectRideSocket", Tag: "ride",
			Summary: "Open the ride WebSocket",
			Description: "Upgrades to a WebSocket that streams the bike status and ride ticks and accepts ride commands. " +
				"Offer the `wattson.v1` subprotocol (400 without it); authenticate with the session cookie (the Origin header must name " +
				"this server or the public URL, else 403) or by also offering the subprotocol `bearer.<token>`. At most 32 connections " +
				"are open at once, 8 per user (503 beyond that). The message formats are in the AsyncAPI document, docs/asyncapi.yaml.",
			Scope: auth.ScopeRead, Status: http.StatusSwitchingProtocols,
			Errors: []int{http.StatusBadRequest, http.StatusServiceUnavailable},
		}}
	})
}

// offersProtocol reports whether the client offered the Wattson subprotocol.
func offersProtocol(r *http.Request) bool {
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if strings.TrimSpace(p) == auth.WSProtocol {
				return true
			}
		}
	}
	return false
}

// originPatterns lists the extra Origin hosts accepted besides the request's
// own host: the public URL's.
func (d Deps) originPatterns(ctx context.Context) []string {
	if d.Settings == nil {
		return nil
	}
	s, err := d.Settings.Get(ctx)
	if err != nil {
		return nil
	}
	if u, err := url.Parse(s.General.PublicURL); err == nil && u.Host != "" {
		return []string{u.Host}
	}
	return nil
}

func (d Deps) serveWS(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		writeEnvelope(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !id.Has(auth.ScopeRead) {
		writeEnvelope(w, http.StatusForbidden, "forbidden", "missing required scope: read")
		return
	}
	if !offersProtocol(r) {
		writeEnvelope(w, http.StatusBadRequest, "invalid_request", "offer the "+auth.WSProtocol+" subprotocol")
		return
	}
	if d.Bike == nil || d.ws == nil {
		writeEnvelope(w, http.StatusServiceUnavailable, "no_bike_manager", noBikeMsg)
		return
	}
	release, reason, ok := d.ws.acquire(id.User.ID)
	if !ok {
		w.Header().Set("Retry-After", "5")
		writeEnvelope(w, http.StatusServiceUnavailable, "too_many_connections", reason)
		return
	}
	defer release()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{auth.WSProtocol},
		OriginPatterns: d.originPatterns(r.Context()),
	})
	if err != nil {
		return // Accept already answered (403 for a foreign origin)
	}
	d.runWS(conn, r, id)
}

// wsSession is one open connection.
type wsSession struct {
	d      Deps
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	id     atomic.Pointer[auth.Identity]
}

func (s *wsSession) send(msg any) bool {
	b, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(s.ctx, wsWriteTimeout)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, b); err != nil {
		s.cancel() // a stalled or dead client: drop the connection, never the hub
		return false
	}
	return true
}

func (d Deps) runWS(conn *websocket.Conn, r *http.Request, id auth.Identity) {
	ctx, cancel := context.WithCancel(r.Context())
	s := &wsSession{d: d, conn: conn, ctx: ctx, cancel: cancel}
	s.id.Store(&id)
	conn.SetReadLimit(wsReadLimit)

	events, unsub := d.Bike.Subscribe(wsEventBuf)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.CloseNow()
		unsub()
		wg.Wait()
	}()

	// Subscribe's first two events are the current status and tick.
	st, ok1 := <-events
	tk, ok2 := <-events
	if !ok1 || !ok2 || st.Status == nil || tk.Tick == nil {
		_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	if !s.send(WSState{Type: "state", Bike: *st.Status, Tick: *tk.Tick}) {
		return
	}

	wg.Add(3)
	go func() { defer wg.Done(); s.readLoop() }()
	go func() { defer wg.Done(); s.pingLoop() }()
	go func() { defer wg.Done(); s.recheckLoop(r) }()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
				return
			}
			switch {
			case ev.Type == "tick" && ev.Tick != nil:
				s.send(WSTick{Type: "tick", Tick: *ev.Tick})
			case ev.Type == "status" && ev.Status != nil:
				s.send(WSBike{Type: "bike", Bike: *ev.Status})
			}
		}
	}
}

func (s *wsSession) pingLoop() {
	t := time.NewTicker(wsPingEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(s.ctx, wsWriteTimeout)
			err := s.conn.Ping(ctx)
			cancel()
			if err != nil {
				s.cancel()
				return
			}
		}
	}
}

// recheckLoop re-validates the credential and closes the connection with
// 4401 once it is no longer good.
func (s *wsSession) recheckLoop(r *http.Request) {
	every := s.d.WSRecheck
	if every <= 0 {
		every = wsDefaultCheck
	}
	if s.d.Auth == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			id, ok := s.d.Auth.Identify(r.WithContext(s.ctx))
			if s.ctx.Err() != nil {
				return
			}
			if !ok || !id.Has(auth.ScopeRead) {
				_ = s.conn.Close(wsUnauthorized, "unauthorized")
				s.cancel()
				return
			}
			s.id.Store(&id)
		}
	}
}

func (s *wsSession) readLoop() {
	for {
		typ, data, err := s.conn.Read(s.ctx)
		if err != nil {
			s.cancel()
			return
		}
		if typ != websocket.MessageText {
			s.send(wsErr("", "invalid_request", "messages must be JSON text"))
			continue
		}
		s.handle(data)
	}
}

func wsErr(id, code, msg string) WSError {
	return WSError{Type: "error", ID: id, Error: ErrorBody{Code: code, Message: msg}}
}

func (s *wsSession) handle(data []byte) {
	var m WSCommand
	if err := json.Unmarshal(data, &m); err != nil {
		s.send(wsErr("", "invalid_request", "message is not valid JSON of the expected shape"))
		return
	}
	if m.Type != "command" {
		s.send(wsErr(m.ID, "invalid_request", `type: must be "command"`))
		return
	}
	if !s.id.Load().Has(auth.ScopeControl) {
		s.send(wsErr(m.ID, "forbidden", "missing required scope: control"))
		return
	}
	if m.Command == "set_target_power" {
		s.send(wsErr(m.ID, "unsupported_command", "set_target_power is not supported yet"))
		return
	}
	kind, known := rideKinds[m.Command]
	if !known {
		s.send(wsErr(m.ID, "unknown_command", "unknown command: "+m.Command))
		return
	}
	if m.RouteID != 0 && kind != engine.CmdStart {
		s.send(wsErr(m.ID, "invalid_request", errRouteIDOnlyStart))
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, wsCommandTimeout)
	defer cancel()
	if err := runCommand(ctx, s.d.Bike, kind, m.Value, m.RouteID); err != nil {
		if _, code, msg, ok := commandFailure(err); ok {
			s.send(wsErr(m.ID, code, msg))
		} else {
			s.d.Logger.Error("ws command failed", "command", m.Command, "err", err)
			s.send(wsErr(m.ID, "internal_error", "internal error"))
		}
		return
	}
	s.send(WSAck{Type: "ack", ID: m.ID})
}
