// Package bluetest provides an in-memory blue.Transport and a reusable contract suite.
package bluetest

import (
	"context"
	"fmt"
	"sync"

	"github.com/metril/wattson/internal/blue"
)

// Device describes a simulated peripheral.
type Device struct {
	Adv      blue.Advertisement
	Services []blue.Service
	// Values are returned by Conn.Read, keyed by characteristic UUID.
	Values map[blue.UUID][]byte
	// OnWrite, if set, is called synchronously for every write to the device.
	OnWrite func(ch blue.UUID, data []byte)
}

// Fake is an in-memory blue.Transport. It spawns no goroutines of its own.
type Fake struct {
	mu     sync.Mutex
	devs   map[blue.Address]*Device
	conns  map[blue.Address]map[*fakeConn]struct{}
	writes map[blue.Address][][]byte
	quit   chan struct{}
	closed bool
}

var _ blue.Transport = (*Fake)(nil)

func NewFake(devs ...*Device) *Fake {
	f := &Fake{
		devs:   map[blue.Address]*Device{},
		conns:  map[blue.Address]map[*fakeConn]struct{}{},
		writes: map[blue.Address][][]byte{},
		quit:   make(chan struct{}),
	}
	for _, d := range devs {
		f.devs[d.Adv.Addr] = d
	}
	return f
}

func (f *Fake) Scan(ctx context.Context, fn func(blue.Advertisement)) error {
	f.mu.Lock()
	advs := make([]blue.Advertisement, 0, len(f.devs))
	for _, d := range f.devs {
		advs = append(advs, d.Adv)
	}
	f.mu.Unlock()
	for _, a := range advs {
		if ctx.Err() != nil {
			return nil
		}
		fn(a)
	}
	select {
	case <-ctx.Done():
	case <-f.quit:
	}
	return nil
}

// Connect returns an error for unknown devices only after ctx is done, like a real
// radio that keeps waiting for an advertisement.
func (f *Fake) Connect(ctx context.Context, addr blue.Address) (blue.Conn, error) {
	f.mu.Lock()
	dev, ok := f.devs[addr]
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("connect %s: transport closed: %w", addr, blue.ErrDisconnected)
	}
	if !ok {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect %s: %w", addr, ctx.Err())
		case <-f.quit:
			return nil, fmt.Errorf("connect %s: transport closed: %w", addr, blue.ErrDisconnected)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	c := &fakeConn{f: f, addr: addr, dev: dev, done: make(chan struct{}), subs: map[blue.UUID][]func([]byte){}}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, fmt.Errorf("connect %s: transport closed: %w", addr, blue.ErrDisconnected)
	}
	if f.conns[addr] == nil {
		f.conns[addr] = map[*fakeConn]struct{}{}
	}
	f.conns[addr][c] = struct{}{}
	f.mu.Unlock()
	return c, nil
}

// Close ends all connections (as local closes) and unblocks Scan/Connect.
func (f *Fake) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	close(f.quit)
	var all []*fakeConn
	for _, m := range f.conns {
		for c := range m {
			all = append(all, c)
		}
	}
	f.mu.Unlock()
	for _, c := range all {
		c.finish(nil)
	}
	return nil
}

// Notify pushes data to every subscriber of ch on addr, synchronously.
func (f *Fake) Notify(addr blue.Address, ch blue.UUID, data []byte) {
	f.mu.Lock()
	var cs []*fakeConn
	for c := range f.conns[addr] {
		cs = append(cs, c)
	}
	f.mu.Unlock()
	for _, c := range cs {
		c.mu.Lock()
		fns := append([]func([]byte){}, c.subs[ch]...)
		c.mu.Unlock()
		for _, fn := range fns {
			fn(append([]byte(nil), data...))
		}
	}
}

// DropLink simulates a remote disconnect of all connections to addr.
func (f *Fake) DropLink(addr blue.Address) {
	f.mu.Lock()
	var cs []*fakeConn
	for c := range f.conns[addr] {
		cs = append(cs, c)
	}
	f.mu.Unlock()
	for _, c := range cs {
		c.finish(fmt.Errorf("link to %s dropped: %w", addr, blue.ErrDisconnected))
	}
}

// Writes returns a copy of every payload written to addr, in order.
func (f *Fake) Writes(addr blue.Address) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.writes[addr]))
	for i, w := range f.writes[addr] {
		out[i] = append([]byte(nil), w...)
	}
	return out
}

type fakeConn struct {
	f    *Fake
	addr blue.Address
	dev  *Device
	done chan struct{}

	mu     sync.Mutex
	closed bool
	err    error
	subs   map[blue.UUID][]func([]byte)
}

func (c *fakeConn) Services() []blue.Service { return c.dev.Services }
func (c *fakeConn) Done() <-chan struct{}    { return c.done }

func (c *fakeConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *fakeConn) Close() error {
	c.finish(nil)
	return nil
}

// finish closes the connection once; the first caller's err wins.
func (c *fakeConn) finish(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.err = true, err
	close(c.done)
	c.mu.Unlock()
	c.f.mu.Lock()
	delete(c.f.conns[c.addr], c)
	c.f.mu.Unlock()
}

// check returns an error if ctx is done or the link is down.
func (c *fakeConn) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return blue.ErrDisconnected
	default:
		return nil
	}
}

func (c *fakeConn) find(ch blue.UUID) (blue.Characteristic, bool) {
	for _, s := range c.dev.Services {
		for _, x := range s.Chars {
			if x.UUID == ch {
				return x, true
			}
		}
	}
	return blue.Characteristic{}, false
}

func (c *fakeConn) Read(ctx context.Context, ch blue.UUID) ([]byte, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	v, ok := c.dev.Values[ch]
	if !ok {
		return nil, fmt.Errorf("read %s: %w", ch, blue.ErrNoCharacteristic)
	}
	return append([]byte(nil), v...), nil
}

func (c *fakeConn) Write(ctx context.Context, ch blue.UUID, data []byte, withResponse bool) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	if _, ok := c.find(ch); !ok {
		return fmt.Errorf("write %s: %w", ch, blue.ErrNoCharacteristic)
	}
	cp := append([]byte(nil), data...)
	c.f.mu.Lock()
	c.f.writes[c.addr] = append(c.f.writes[c.addr], cp)
	c.f.mu.Unlock()
	if c.dev.OnWrite != nil {
		c.dev.OnWrite(ch, append([]byte(nil), data...))
	}
	return nil
}

func (c *fakeConn) Subscribe(ctx context.Context, ch blue.UUID, fn func([]byte)) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	x, ok := c.find(ch)
	if !ok {
		return fmt.Errorf("subscribe %s: %w", ch, blue.ErrNoCharacteristic)
	}
	if x.Props&(blue.PropNotify|blue.PropIndicate) == 0 {
		return fmt.Errorf("subscribe %s: characteristic cannot notify or indicate", ch)
	}
	c.mu.Lock()
	c.subs[ch] = append(c.subs[ch], fn)
	c.mu.Unlock()
	return nil
}
