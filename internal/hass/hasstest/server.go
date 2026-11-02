// Package hasstest is a fake Home Assistant WebSocket server for tests. It
// speaks the real protocol: the auth phase, message ids, get_states,
// call_service, ping/pong and subscribe_entities with compressed diffs.
package hasstest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Version is the Home Assistant version the fake reports.
const Version = "2026.10.0"

// Call is one recorded call_service.
type Call struct {
	Domain, Service, EntityID string
	Data                      map[string]any
}

type entity struct {
	state string
	attrs map[string]any
}

type conn struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	subs map[int]map[string]struct{} // subscription id -> entity ids (empty = all); guarded by Server.mu
	last int                         // highest request id seen; guarded by Server.mu
}

// Server is a running fake.
type Server struct {
	token string
	hs    *httptest.Server

	mu          sync.Mutex
	ents        map[string]*entity
	conns       map[*conn]struct{}
	calls       []Call
	failCode    string
	failMsg     string
	ignorePings bool
	auths       int
}

// Start runs a fake accepting token; it stops with the test.
func Start(t testing.TB, token string) *Server {
	s := &Server{token: token, ents: map[string]*entity{}, conns: map[*conn]struct{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/websocket", s.serve)
	s.hs = httptest.NewServer(mux)
	t.Cleanup(func() {
		s.Drop()
		s.hs.Close()
	})
	return s
}

// URL is the http base URL of the fake.
func (s *Server) URL() string { return s.hs.URL }

// Calls returns the call_service requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// FailCalls makes every following call_service fail with code and msg; an empty code restores success.
func (s *Server) FailCalls(code, msg string) {
	s.mu.Lock()
	s.failCode, s.failMsg = code, msg
	s.mu.Unlock()
}

// IgnorePings stops answering pings while on.
func (s *Server) IgnorePings(on bool) {
	s.mu.Lock()
	s.ignorePings = on
	s.mu.Unlock()
}

// Auths counts successful authentications so far.
func (s *Server) Auths() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auths
}

// Drop closes every open connection.
func (s *Server) Drop() {
	s.mu.Lock()
	cs := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		_ = c.ws.CloseNow()
	}
}

func (c *conn) send(v any) {
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *conn) watches(sub map[string]struct{}, id string) bool {
	if len(sub) == 0 {
		return true
	}
	_, ok := sub[id]
	return ok
}

func compressed(e *entity) map[string]any {
	now := float64(time.Now().UnixNano()) / 1e9
	a := map[string]any{}
	for k, v := range e.attrs {
		a[k] = v
	}
	return map[string]any{"s": e.state, "a": a, "c": "ctx", "lc": now, "lu": now}
}

// push sends ev to every subscription watching id. Caller holds s.mu.
func (s *Server) push(id string, ev map[string]any) {
	for c := range s.conns {
		for sid, set := range c.subs {
			if c.watches(set, id) {
				c.send(map[string]any{"id": sid, "type": "event", "event": ev})
			}
		}
	}
}

// SetState sets an entity's state and merges attrs into its attributes, then
// pushes the change as a compressed diff (or an add for a new entity) to
// subscribers. Nothing is pushed when nothing changed.
func (s *Server) SetState(id, state string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(id, state, attrs)
}

func (s *Server) setLocked(id, state string, attrs map[string]any) {
	e, ok := s.ents[id]
	if !ok {
		e = &entity{state: state, attrs: map[string]any{}}
		for k, v := range attrs {
			e.attrs[k] = v
		}
		s.ents[id] = e
		s.push(id, map[string]any{"a": map[string]any{id: compressed(e)}})
		return
	}
	plus := map[string]any{}
	if e.state != state {
		plus["s"] = state
		e.state = state
	}
	changed := map[string]any{}
	for k, v := range attrs {
		if old, had := e.attrs[k]; !had || !reflect.DeepEqual(old, v) {
			changed[k] = v
			e.attrs[k] = v
		}
	}
	if len(changed) > 0 {
		plus["a"] = changed
	}
	if len(plus) == 0 {
		return
	}
	plus["lu"] = float64(time.Now().UnixNano()) / 1e9
	s.push(id, map[string]any{"c": map[string]any{id: map[string]any{"+": plus}}})
}

// RemoveAttrs deletes attribute keys and pushes the "-" diff.
func (s *Server) RemoveAttrs(id string, keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.ents[id]
	if !ok {
		return
	}
	var gone []string
	for _, k := range keys {
		if _, had := e.attrs[k]; had {
			delete(e.attrs, k)
			gone = append(gone, k)
		}
	}
	if len(gone) > 0 {
		s.push(id, map[string]any{"c": map[string]any{id: map[string]any{"-": map[string]any{"a": gone}}}})
	}
}

// RemoveState deletes an entity and pushes the "r" event.
func (s *Server) RemoveState(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ents[id]; !ok {
		return
	}
	delete(s.ents, id)
	s.push(id, map[string]any{"r": []string{id}})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(1 << 20)
	c := &conn{ws: ws, subs: map[int]map[string]struct{}{}}
	defer ws.CloseNow()
	ctx := r.Context()

	c.send(map[string]any{"type": "auth_required", "ha_version": Version})
	var in struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
	}
	_, data, err := ws.Read(ctx)
	if err != nil || json.Unmarshal(data, &in) != nil || in.Type != "auth" {
		return
	}
	if in.AccessToken != s.token {
		c.send(map[string]any{"type": "auth_invalid", "message": "Invalid access token or password"})
		return
	}
	s.mu.Lock()
	s.auths++
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	c.send(map[string]any{"type": "auth_ok", "ha_version": Version})
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()

	ws.SetReadLimit(16 << 20)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(data, &m) != nil {
			return
		}
		s.handle(c, m)
	}
}

func str(m map[string]json.RawMessage, k string) string {
	var v string
	_ = json.Unmarshal(m[k], &v)
	return v
}

func (s *Server) handle(c *conn, m map[string]json.RawMessage) {
	var id int
	_ = json.Unmarshal(m["id"], &id)
	typ := str(m, "type")
	ok := func(result any) {
		c.send(map[string]any{"id": id, "type": "result", "success": true, "result": result})
	}
	fail := func(code, msg string) {
		c.send(map[string]any{"id": id, "type": "result", "success": false, "error": map[string]string{"code": code, "message": msg}})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Like Home Assistant, ids must strictly increase per connection.
	if id <= c.last {
		fail("id_reuse", "Identifier values have to increase.")
		return
	}
	c.last = id
	switch typ {
	case "ping":
		if !s.ignorePings {
			c.send(map[string]any{"id": id, "type": "pong"})
		}
	case "get_states":
		out := make([]map[string]any, 0, len(s.ents))
		for eid, e := range s.ents {
			out = append(out, map[string]any{"entity_id": eid, "state": e.state, "attributes": e.attrs})
		}
		ok(out)
	case "subscribe_entities":
		var ids []string
		_ = json.Unmarshal(m["entity_ids"], &ids)
		set := map[string]struct{}{}
		for _, e := range ids {
			set[e] = struct{}{}
		}
		c.subs[id] = set
		ok(nil)
		added := map[string]any{}
		for eid, e := range s.ents {
			if c.watches(set, eid) {
				added[eid] = compressed(e)
			}
		}
		c.send(map[string]any{"id": id, "type": "event", "event": map[string]any{"a": added}})
	case "unsubscribe_events":
		var sub int
		_ = json.Unmarshal(m["subscription"], &sub)
		delete(c.subs, sub)
		ok(nil)
	case "call_service":
		s.callService(m, ok, fail)
	default:
		fail("unknown_command", "Unknown command.")
	}
}

func (s *Server) callService(m map[string]json.RawMessage, ok func(any), fail func(code, msg string)) {
	call := Call{Domain: str(m, "domain"), Service: str(m, "service")}
	_ = json.Unmarshal(m["service_data"], &call.Data)
	var target struct {
		EntityID json.RawMessage `json:"entity_id"`
	}
	_ = json.Unmarshal(m["target"], &target)
	var one string
	var many []string
	if json.Unmarshal(target.EntityID, &one) != nil && json.Unmarshal(target.EntityID, &many) == nil && len(many) > 0 {
		one = many[0]
	}
	call.EntityID = one
	s.calls = append(s.calls, call)
	if s.failCode != "" {
		fail(s.failCode, s.failMsg)
		return
	}
	if call.Domain == "number" && call.Service == "set_value" {
		if v, isNum := call.Data["value"].(float64); isNum {
			if _, exists := s.ents[call.EntityID]; exists {
				s.setLocked(call.EntityID, strconv.FormatFloat(v, 'f', -1, 64), nil)
			}
		}
	}
	ok(map[string]any{"context": map[string]any{"id": "ctx"}})
}
