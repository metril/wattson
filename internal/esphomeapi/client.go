package esphomeapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrTimeout means the device stopped answering keepalive pings.
	ErrTimeout = errors.New("esphomeapi: ping timeout")
	// ErrDisconnected means the device asked us to disconnect.
	ErrDisconnected = errors.New("esphomeapi: disconnected by server")
	// ErrClosed is returned by Send after the client has closed.
	ErrClosed = errors.New("esphomeapi: client closed")
)

const (
	defaultPingInterval = 10 * time.Second
	clientInfo          = "wattson"
	apiMajor, apiMinor  = 1, 10
	closeWriteTimeout   = time.Second
	minWriteTimeout     = time.Second
)

// writeTimeout is the per-write deadline for a keepalive interval: 2x the
// interval, at least one second.
func writeTimeout(interval time.Duration) time.Duration {
	return max(2*interval, minWriteTimeout)
}

// DialOptions tunes Dial. The zero value is production-ready.
type DialOptions struct {
	// PingInterval is the keepalive period (default 10 s). A ping that sees no
	// inbound traffic within 2x the interval closes the client with ErrTimeout.
	PingInterval time.Duration
}

// DeviceInfo is the identity reported by the device.
type DeviceInfo struct {
	Name, MAC, ESPHomeVersion string
	BTProxyFlags              uint32
}

// Client is a connected, hello-completed ESPHome API session.
type Client struct {
	sc       *secureConn
	interval time.Duration

	info DeviceInfo // set before Dial returns; read-only afterwards

	hmu      sync.RWMutex
	handlers map[uint16]map[uint64]func([]byte)
	nextID   uint64

	opMu  sync.Mutex // guards opSem
	opSem map[opKey]chan struct{}

	rx   chan struct{} // inbound-traffic signal for the keepalive loop
	done chan struct{}
	once sync.Once
	err  atomic.Pointer[error]
	wg   sync.WaitGroup
}

// Dial connects to hostport (host:port), performs the Noise handshake with the
// base64 PSK key, then Hello and DeviceInfo. ctx bounds the whole setup.
func Dial(ctx context.Context, hostport, key string, opts ...DialOptions) (*Client, error) {
	var o DialOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.PingInterval <= 0 {
		o.PingInterval = defaultPingInterval
	}
	psk, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("%w: key is not valid base64", ErrBadKey)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return nil, err
	}
	sc, _, err := clientHandshake(ctx, conn, psk)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	sc.wtimeout = writeTimeout(o.PingInterval)
	c := &Client{
		sc: sc, interval: o.PingInterval,
		handlers: map[uint16]map[uint64]func([]byte){},
		rx:       make(chan struct{}, 1),
		done:     make(chan struct{}),
	}

	helloCh := make(chan error, 1)
	infoCh := make(chan error, 1)
	gotInfo := false // touched only by the read goroutine
	cancelHello := c.On(MsgHelloResponse, func(p []byte) {
		var h HelloResponse
		select {
		case helloCh <- h.Unmarshal(p):
		default: // duplicate response
		}
	})
	cancelInfo := c.On(MsgDeviceInfoResponse, func(p []byte) {
		var r DeviceInfoResponse
		err := r.Unmarshal(p)
		if gotInfo { // duplicate response
			return
		}
		gotInfo = true
		// Written before the channel send, which publishes it to Dial.
		c.info = DeviceInfo{Name: r.Name, MAC: r.MACAddress, ESPHomeVersion: r.ESPHomeVersion, BTProxyFlags: r.BTProxyFeatureFlags}
		infoCh <- err
	})
	stop := context.AfterFunc(ctx, func() { c.closeWith(ctx.Err()) })

	c.wg.Add(1)
	go c.readLoop()

	fail := func(err error) (*Client, error) {
		stop()
		c.closeWith(err)
		c.wg.Wait()
		if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
			err = fmt.Errorf("%w: %w", cerr, err)
		}
		return nil, err
	}
	step := func(req uint16, m interface{ Marshal() []byte }, ch chan error, what string) error {
		if err := c.Send(req, m); err != nil {
			return err
		}
		select {
		case err := <-ch:
			if err != nil {
				return fmt.Errorf("esphomeapi: %s: %w", what, err)
			}
			return nil
		case <-c.done:
			return c.Err()
		}
	}
	if err := step(MsgHelloRequest, HelloRequest{ClientInfo: clientInfo, APIVersionMajor: apiMajor, APIVersionMinor: apiMinor}, helloCh, "hello"); err != nil {
		return fail(err)
	}
	if err := step(MsgDeviceInfoRequest, Empty{}, infoCh, "device info"); err != nil {
		return fail(err)
	}
	cancelHello()
	cancelInfo()
	if !stop() { // ctx fired right at the end; closeWith is in flight
		return fail(ctx.Err())
	}

	c.wg.Add(1)
	go c.pingLoop()
	return c, nil
}

// Info returns the identity reported by the device.
func (c *Client) Info() DeviceInfo { return c.info }

// Done is closed when the client has terminated for any reason.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the client terminated: nil while running or after a local Close,
// ErrTimeout, ErrDisconnected, or the underlying I/O error.
func (c *Client) Err() error {
	if p := c.err.Load(); p != nil {
		return *p
	}
	return nil
}

// Send marshals and sends one message. A write that fails or exceeds the write
// deadline (2x the ping interval, at least 1 s) closes the client, since a
// partial write leaves the encrypted stream unusable.
func (c *Client) Send(typ uint16, m interface{ Marshal() []byte }) error {
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if err := c.sc.WriteMsg(typ, m.Marshal()); err != nil {
		select {
		case <-c.done:
			return ErrClosed
		default:
		}
		c.closeWith(err)
		return err
	}
	return nil
}

// On registers fn for every inbound message of type typ and returns a cancel
// func. Handlers run on the read goroutine, in unspecified order relative to
// each other for one message: they must never block (hand off with a
// non-blocking send), must not wait on the connection, and must not call Close.
func (c *Client) On(typ uint16, fn func([]byte)) (cancel func()) {
	c.hmu.Lock()
	id := c.nextID
	c.nextID++
	m := c.handlers[typ]
	if m == nil {
		m = map[uint64]func([]byte){}
		c.handlers[typ] = m
	}
	m[id] = fn
	c.hmu.Unlock()
	return func() {
		c.hmu.Lock()
		delete(c.handlers[typ], id)
		c.hmu.Unlock()
	}
}

// Close sends DisconnectRequest, closes the connection and waits for the
// client's goroutines to exit. It is idempotent.
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		_ = c.sc.writeMsg(MsgDisconnectRequest, nil, closeWriteTimeout)
		c.closeWith(nil)
	}
	c.wg.Wait()
	return nil
}

// closeWith terminates the client once, recording err as the reason.
func (c *Client) closeWith(err error) {
	c.once.Do(func() {
		if err != nil {
			c.err.Store(&err)
		}
		close(c.done)
		_ = c.sc.Close()
	})
}

func (c *Client) dispatch(typ uint16, payload []byte) {
	c.hmu.RLock()
	fns := make([]func([]byte), 0, len(c.handlers[typ]))
	for _, fn := range c.handlers[typ] {
		fns = append(fns, fn)
	}
	c.hmu.RUnlock()
	for _, fn := range fns {
		fn(payload)
	}
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	for {
		typ, payload, err := c.sc.ReadMsg()
		if err != nil {
			c.closeWith(err)
			return
		}
		select {
		case c.rx <- struct{}{}:
		default:
		}
		switch typ {
		case MsgPingRequest:
			_ = c.sc.WriteMsg(MsgPingResponse, nil)
		case MsgDisconnectRequest:
			_ = c.sc.WriteMsg(MsgDisconnectResponse, nil)
			c.dispatch(typ, payload)
			c.closeWith(ErrDisconnected)
			return
		}
		c.dispatch(typ, payload)
	}
}

// pingLoop sends a PingRequest every interval and closes with ErrTimeout if no
// inbound traffic arrives within 2x the interval after it.
func (c *Client) pingLoop() {
	defer c.wg.Done()
	t := time.NewTimer(c.interval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		select { // discard stale activity so only post-ping traffic counts
		case <-c.rx:
		default:
		}
		if err := c.Send(MsgPingRequest, Empty{}); err != nil {
			c.closeWith(err)
			return
		}
		t.Reset(2 * c.interval)
		select {
		case <-c.done:
			return
		case <-c.rx:
			t.Reset(c.interval) // Go >= 1.23: Reset also discards a pending fire
		case <-t.C:
			c.closeWith(ErrTimeout)
			return
		}
	}
}
