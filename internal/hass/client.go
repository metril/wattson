// Package hass is a Home Assistant WebSocket API client. It never logs or
// returns the access token.
package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var (
	// ErrAuth means Home Assistant rejected the access token.
	ErrAuth = errors.New("home assistant: access token rejected")
	// ErrClosed means the connection is closed (by either side).
	ErrClosed = errors.New("home assistant: connection closed")
	// ErrTimeout means the server stopped answering pings.
	ErrTimeout = errors.New("home assistant: ping timeout")
)

// State is one entity state.
type State struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// ResultError is a failed request result.
type ResultError struct{ Code, Message string }

func (e *ResultError) Error() string {
	return "home assistant: " + e.Code + ": " + e.Message
}

// Options tunes a Client. Zero values select the defaults.
type Options struct {
	// PingInterval is how often a ping is sent; a ping unanswered for twice
	// this long closes the client with ErrTimeout. Default 10s.
	PingInterval time.Duration
	// ReadLimit is the largest accepted message in bytes. Default 1 MiB; raise
	// it (16 MiB) for States on large installations.
	ReadLimit int64
}

const (
	defaultPing     = 10 * time.Second
	defaultReadLim  = 1 << 20
	handshakeBound  = 15 * time.Second
	writeBound      = 10 * time.Second
	initialEventMax = 10 * time.Second
)

// Client is one authenticated connection. All methods are safe for concurrent use.
type Client struct {
	conn    *websocket.Conn
	version string
	ctx     context.Context
	cancel  context.CancelFunc

	wmu sync.Mutex // serialises writes

	mu      sync.Mutex
	nextID  int
	pending map[int]chan reply
	subs    map[int]*entSub

	done chan struct{}
	err  error
	once sync.Once
}

type reply struct {
	result json.RawMessage
	err    error
}

type incoming struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Event     json.RawMessage `json:"event"`
	HAVersion string          `json:"ha_version"`
}

// Dial connects to the Home Assistant at base (an http or https URL), runs the
// auth phase with token and starts the read and keepalive loops. ctx bounds
// only the connection setup; the Client outlives it until Close.
func Dial(ctx context.Context, base, token string, o Options) (*Client, error) {
	u, err := WSURL(base)
	if err != nil {
		return nil, err
	}
	if o.PingInterval <= 0 {
		o.PingInterval = defaultPing
	}
	if o.ReadLimit <= 0 {
		o.ReadLimit = defaultReadLim
	}
	hctx, cancel := context.WithTimeout(ctx, handshakeBound)
	defer cancel()
	conn, _, err := websocket.Dial(hctx, u, nil)
	if err != nil {
		return nil, fmt.Errorf("home assistant: connect: %w", err)
	}
	conn.SetReadLimit(o.ReadLimit)
	version, err := authenticate(hctx, conn, token)
	if err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	cctx, ccancel := context.WithCancel(context.Background())
	c := &Client{
		conn: conn, version: version, ctx: cctx, cancel: ccancel,
		pending: map[int]chan reply{}, subs: map[int]*entSub{}, done: make(chan struct{}),
	}
	go c.readLoop()
	go c.pingLoop(o.PingInterval)
	return c, nil
}

func readMsg(ctx context.Context, conn *websocket.Conn) (incoming, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return incoming{}, err
	}
	var m incoming
	if typ != websocket.MessageText || json.Unmarshal(data, &m) != nil {
		return incoming{}, errors.New("malformed message")
	}
	return m, nil
}

func authenticate(ctx context.Context, conn *websocket.Conn, token string) (string, error) {
	m, err := readMsg(ctx, conn)
	if err != nil || m.Type != "auth_required" {
		return "", fmt.Errorf("home assistant: expected auth_required: %w", orErr(err, errors.New("unexpected message")))
	}
	b, _ := json.Marshal(map[string]string{"type": "auth", "access_token": token})
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return "", fmt.Errorf("home assistant: send auth: %w", err)
	}
	m, err = readMsg(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("home assistant: read auth result: %w", err)
	}
	switch m.Type {
	case "auth_ok":
		return m.HAVersion, nil
	case "auth_invalid":
		return "", ErrAuth
	}
	return "", fmt.Errorf("home assistant: unexpected %q during auth", m.Type)
}

func orErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// HAVersion is the Home Assistant version reported at auth.
func (c *Client) HAVersion() string { return c.version }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why Done closed (wrapping ErrClosed or ErrTimeout); nil while open.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close ends the connection. It must not be called from a SubscribeEntities callback.
func (c *Client) Close() error {
	select {
	case <-c.done:
		return nil
	default:
	}
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.fail(ErrClosed)
	return nil
}

func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
		c.cancel()
		_ = c.conn.CloseNow()
	})
}

func (c *Client) readLoop() {
	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.fail(fmt.Errorf("%w: %v", ErrClosed, err))
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m incoming
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "result", "pong":
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			r := reply{result: m.Result}
			if m.Type == "result" && !m.Success {
				re := &ResultError{Code: "unknown", Message: "request failed"}
				if m.Error != nil {
					re.Code, re.Message = m.Error.Code, m.Error.Message
				}
				r.err = re
			}
			ch <- r
		case "event":
			c.mu.Lock()
			s := c.subs[m.ID]
			c.mu.Unlock()
			if s != nil {
				s.handle(m.Event)
			}
		}
	}
}

func (c *Client) pingLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(c.ctx, 2*every)
		_, err := c.do(pctx, "ping", nil, nil)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			c.fail(ErrTimeout)
			return
		}
		if err != nil {
			return
		}
	}
}

// do sends one request and waits for its result or pong. onID, when set, runs
// with the allocated id before the request is written.
func (c *Client) do(ctx context.Context, typ string, fields map[string]any, onID func(id int)) (json.RawMessage, error) {
	ch := make(chan reply, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, c.err
	default:
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if onID != nil {
		onID(id)
	}
	msg := map[string]any{"id": id, "type": typ}
	maps.Copy(msg, fields)
	b, err := json.Marshal(msg)
	if err != nil {
		forget()
		return nil, err
	}
	wctx, cancel := context.WithTimeout(c.ctx, writeBound)
	c.wmu.Lock()
	err = c.conn.Write(wctx, websocket.MessageText, b)
	c.wmu.Unlock()
	cancel()
	if err != nil {
		forget()
		c.fail(fmt.Errorf("%w: write: %v", ErrClosed, err))
		return nil, c.err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// States returns every entity state (get_states). Raise Options.ReadLimit for
// large installations.
func (c *Client) States(ctx context.Context) ([]State, error) {
	raw, err := c.do(ctx, "get_states", nil, nil)
	if err != nil {
		return nil, err
	}
	var out []State
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("home assistant: decode states: %w", err)
	}
	return out, nil
}

// CallService calls domain.service, targeting entityID when it is not "".
func (c *Client) CallService(ctx context.Context, domain, service, entityID string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	f := map[string]any{"domain": domain, "service": service, "service_data": data, "return_response": false}
	if entityID != "" {
		f["target"] = map[string]string{"entity_id": entityID}
	}
	_, err := c.do(ctx, "call_service", f, nil)
	return err
}

// SubscribeEntities subscribes to ids (all entities when empty). fn receives
// each entity's full state: first as added, then after every merged diff. It
// runs on the read goroutine, so it must be quick and must not call the
// Client. SubscribeEntities returns once the initial state event has been
// delivered; the subscription ends (unsubscribe_events) when ctx is done.
func (c *Client) SubscribeEntities(ctx context.Context, ids []string, fn func(State)) error {
	sub := &entSub{fn: fn, states: map[string]State{}, initial: make(chan struct{})}
	f := map[string]any{}
	if len(ids) > 0 {
		f["entity_ids"] = ids
	}
	var sid int
	_, err := c.do(ctx, "subscribe_entities", f, func(id int) {
		sid = id
		c.mu.Lock()
		c.subs[id] = sub
		c.mu.Unlock()
	})
	if err != nil {
		c.dropSub(sid)
		return err
	}
	t := time.NewTimer(initialEventMax)
	defer t.Stop()
	select {
	case <-sub.initial:
	case <-ctx.Done():
		c.unsubscribe(sid)
		return ctx.Err()
	case <-c.done:
		return c.err
	case <-t.C:
		c.unsubscribe(sid)
		return fmt.Errorf("home assistant: no initial state event: %w", ErrTimeout)
	}
	go func() {
		select {
		case <-ctx.Done():
			c.unsubscribe(sid)
		case <-c.done:
		}
	}()
	return nil
}

func (c *Client) dropSub(id int) {
	c.mu.Lock()
	delete(c.subs, id)
	c.mu.Unlock()
}

func (c *Client) unsubscribe(id int) {
	c.dropSub(id)
	uctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	_, _ = c.do(uctx, "unsubscribe_events", map[string]any{"subscription": id}, nil)
}

// entSub merges the compressed subscribe_entities events into full states.
type entSub struct {
	fn      func(State)
	states  map[string]State // read goroutine only
	initial chan struct{}
	once    sync.Once
}

type compEvent struct {
	A map[string]struct {
		S string         `json:"s"`
		A map[string]any `json:"a"`
	} `json:"a"`
	C map[string]struct {
		Plus *struct {
			S *string        `json:"s"`
			A map[string]any `json:"a"`
		} `json:"+"`
		Minus *struct {
			A []string `json:"a"`
		} `json:"-,"` // "-," is the literal key "-"
	} `json:"c"`
	R []string `json:"r"`
}

func (s *entSub) handle(raw json.RawMessage) {
	var ev compEvent
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	for _, id := range slices.Sorted(maps.Keys(ev.A)) {
		a := ev.A[id]
		st := State{EntityID: id, State: a.S, Attributes: maps.Clone(a.A)}
		if st.Attributes == nil {
			st.Attributes = map[string]any{}
		}
		s.states[id] = st
		s.deliver(st)
	}
	for _, id := range slices.Sorted(maps.Keys(ev.C)) {
		d := ev.C[id]
		st, ok := s.states[id]
		if !ok {
			st = State{EntityID: id, Attributes: map[string]any{}}
		}
		st.Attributes = maps.Clone(st.Attributes)
		if d.Plus != nil {
			if d.Plus.S != nil {
				st.State = *d.Plus.S
			}
			maps.Copy(st.Attributes, d.Plus.A)
		}
		if d.Minus != nil {
			for _, k := range d.Minus.A {
				delete(st.Attributes, k)
			}
		}
		s.states[id] = st
		s.deliver(st)
	}
	for _, id := range ev.R {
		delete(s.states, id) // removal has no callback; a later add re-delivers
	}
	s.once.Do(func() { close(s.initial) })
}

func (s *entSub) deliver(st State) {
	st.Attributes = maps.Clone(st.Attributes)
	s.fn(st)
}
