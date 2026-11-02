package blue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/metril/wattson/internal/esphomeapi"
)

const (
	// userQueueLen bounds the per-subscription / per-scan backlog of events
	// waiting for a slow user callback; the oldest are dropped first.
	userQueueLen = 64
	scanQueueLen = 256
	// closeTimeout bounds the disconnect handshake in Conn.Close.
	closeTimeout = time.Second
)

// NewESPHome dials an ESPHome device running bluetooth_proxy over the
// Noise-encrypted native API (key is the base64 PSK) and returns a Transport
// that scans and connects through it.
//
// bluetooth_proxy routes every BLE reply (connection state, GATT services,
// reads, write/notify acks, notification data, GATT errors) only to the API
// client that subscribed to advertisements, and drops them otherwise. The
// transport therefore holds one advertisement subscription for its whole
// lifetime; adverts are ignored unless a Scan is running.
func NewESPHome(ctx context.Context, hostport, key string) (Transport, error) {
	c, err := esphomeapi.Dial(ctx, hostport, key)
	if err != nil {
		return nil, err
	}
	t := &espTransport{c: c, scans: map[*dropQueue[esphomeapi.RawAdv]]struct{}{}}
	// Never cancelled: only Close ends the subscription (see Close).
	if err := c.SubscribeAdvertisements(context.Background(), t.onAdv); err != nil {
		_ = c.Close()
		return nil, espErr("subscribe to proxy", err)
	}
	return t, nil
}

type espTransport struct {
	c *esphomeapi.Client

	mu    sync.RWMutex
	scans map[*dropQueue[esphomeapi.RawAdv]]struct{} // active Scan queues
}

// onAdv runs on the API read goroutine: fan out without blocking.
func (t *espTransport) onAdv(r esphomeapi.RawAdv) {
	t.mu.RLock()
	for q := range t.scans {
		q.push(r)
	}
	t.mu.RUnlock()
}

// dropQueue is a bounded FIFO that drops its oldest entry when full. push
// never blocks, so it is safe on the API client's read goroutine; run delivers
// to a callback on its own goroutine.
type dropQueue[T any] struct {
	mu  sync.Mutex
	q   []T
	max int
	sig chan struct{}
}

func newDropQueue[T any](max int) *dropQueue[T] {
	return &dropQueue[T]{max: max, sig: make(chan struct{}, 1)}
}

func (d *dropQueue[T]) push(v T) {
	d.mu.Lock()
	if len(d.q) >= d.max {
		d.q = append(d.q[:0], d.q[1:]...)
	}
	d.q = append(d.q, v)
	d.mu.Unlock()
	select {
	case d.sig <- struct{}{}:
	default:
	}
}

func (d *dropQueue[T]) pop() (v T, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.q) == 0 {
		return v, false
	}
	v = d.q[0]
	d.q = append(d.q[:0], d.q[1:]...)
	return v, true
}

// run calls fn for queued items until stop is closed.
func (d *dropQueue[T]) run(stop <-chan struct{}, fn func(T)) {
	for {
		select {
		case <-stop:
			return
		case <-d.sig:
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			v, ok := d.pop()
			if !ok {
				break
			}
			fn(v)
		}
	}
}

func addrFromUint64(v uint64, typ uint32) Address {
	var a Address
	for i := 5; i >= 0; i-- {
		a.MAC[i] = byte(v)
		v >>= 8
	}
	// ESPHome address types: 0 public, 1 random, 2/3 resolvable-private
	// variants of public/random.
	if typ == 1 || typ == 3 {
		a.Type = AddrRandom
	}
	return a
}

func (t *espTransport) Scan(ctx context.Context, fn func(Advertisement)) error {
	q := newDropQueue[esphomeapi.RawAdv](scanQueueLen)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		q.run(stop, func(r esphomeapi.RawAdv) {
			name, svcs := ParseAdvertisingData(r.Data)
			fn(Advertisement{Addr: addrFromUint64(r.Address, r.AddressType), Name: name, RSSI: int(r.RSSI), Services: svcs})
		})
	}()
	t.mu.Lock()
	t.scans[q] = struct{}{}
	t.mu.Unlock()
	var err error
	select {
	case <-ctx.Done():
	case <-t.c.Done():
		err = fmt.Errorf("scan: %w: %v", ErrDisconnected, t.c.Err())
	}
	// The lifetime subscription stays: it also carries GATT replies.
	t.mu.Lock()
	delete(t.scans, q)
	t.mu.Unlock()
	close(stop)
	wg.Wait()
	return err
}

func (t *espTransport) Connect(ctx context.Context, addr Address) (Conn, error) {
	op := "connect " + addr.String()
	a := addr.Uint64()
	c := &espConn{t: t, addr: addr, a: a, done: make(chan struct{})}
	// Registered before the connect request so a drop at any later point
	// (including during service discovery) is never missed.
	c.cancelDisc = t.c.OnBLEDisconnect(a, func(err error) {
		c.finish(fmt.Errorf("link to %s lost: %w (%v)", addr, ErrDisconnected, err))
	})
	if _, err := t.c.BLEConnect(ctx, a, uint32(addr.Type)); err != nil {
		c.cancelDisc()
		if errors.Is(err, esphomeapi.ErrNoSlots) {
			return nil, fmt.Errorf("%s: %w", op, ErrNoSlots)
		}
		return nil, espErr(op, err)
	}
	if c.isDone() { // dropped right after connecting; the link is already down
		c.cancelDisc()
		return nil, fmt.Errorf("%s: %w", op, c.Err())
	}
	svcs, err := t.c.GATTServices(ctx, a)
	if err != nil {
		c.cancelDisc()
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = t.c.BLEDisconnect(dctx, a)
		return nil, espErr(op+": discover services", err)
	}
	if c.isDone() {
		c.cancelDisc()
		return nil, fmt.Errorf("%s: %w", op, c.Err())
	}
	c.chars = map[UUID]Characteristic{}
	for _, s := range svcs {
		sv := Service{UUID: uuidFromAPI(s.Full())}
		for _, ch := range s.Characteristics {
			x := Characteristic{UUID: uuidFromAPI(ch.Full()), Handle: uint16(ch.Handle), Props: Props(ch.Properties)}
			sv.Chars = append(sv.Chars, x)
			if _, dup := c.chars[x.UUID]; !dup {
				c.chars[x.UUID] = x
			}
		}
		c.services = append(c.services, sv)
	}
	c.wg.Add(1)
	go func() { // proxy link loss ends every connection through it
		defer c.wg.Done()
		select {
		case <-t.c.Done():
			c.finish(fmt.Errorf("proxy link lost: %w (%v)", ErrDisconnected, t.c.Err()))
		case <-c.done:
		}
	}()
	return c, nil
}

func (t *espTransport) Close() error {
	// Explicit, synchronous Unsubscribe (best effort) before the link goes down.
	_ = t.c.Send(esphomeapi.MsgUnsubscribeBLEAdvertisements, esphomeapi.Empty{})
	return t.c.Close()
}

// espErr annotates err with op, mapping loss of the BLE or proxy link to ErrDisconnected.
func espErr(op string, err error) error {
	var ge *esphomeapi.GATTError
	if errors.Is(err, esphomeapi.ErrBLEDisconnected) || errors.Is(err, esphomeapi.ErrClosed) ||
		(errors.As(err, &ge) && ge.Code == esphomeapi.GATTErrNotConnected) {
		return fmt.Errorf("%s: %w (%v)", op, ErrDisconnected, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func uuidFromAPI(u esphomeapi.UUID) UUID {
	var out UUID
	for i := 0; i < 8; i++ {
		out[i] = byte(u[0] >> (56 - 8*i))
		out[8+i] = byte(u[1] >> (56 - 8*i))
	}
	return out
}

type espConn struct {
	t        *espTransport
	addr     Address
	a        uint64
	services []Service
	chars    map[UUID]Characteristic

	cancelDisc func()
	wg         sync.WaitGroup // link watcher
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	err        error
}

func (c *espConn) Services() []Service   { return c.services }
func (c *espConn) Done() <-chan struct{} { return c.done }

func (c *espConn) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *espConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// finish ends the connection once; it reports whether this call did so.
func (c *espConn) finish(err error) (first bool) {
	c.once.Do(func() {
		first = true
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
	return first
}

func (c *espConn) Close() error {
	first := c.finish(nil)
	c.cancelDisc() // idempotent; also needed after a remote drop
	if first {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		_ = c.t.c.BLEDisconnect(ctx, c.a)
		cancel()
	}
	c.wg.Wait()
	return nil
}

// lookup checks the link is up and resolves ch.
func (c *espConn) lookup(op string, ch UUID) (Characteristic, error) {
	select {
	case <-c.done:
		return Characteristic{}, fmt.Errorf("%s %s: connection closed: %w", op, ch, ErrDisconnected)
	default:
	}
	x, ok := c.chars[ch]
	if !ok {
		return Characteristic{}, fmt.Errorf("%s %s: %w", op, ch, ErrNoCharacteristic)
	}
	return x, nil
}

func (c *espConn) Read(ctx context.Context, ch UUID) ([]byte, error) {
	x, err := c.lookup("read", ch)
	if err != nil {
		return nil, err
	}
	v, err := c.t.c.GATTRead(ctx, c.a, uint32(x.Handle))
	if err != nil {
		return nil, espErr("read "+ch.String(), err)
	}
	return v, nil
}

func (c *espConn) Write(ctx context.Context, ch UUID, data []byte, withResponse bool) error {
	x, err := c.lookup("write", ch)
	if err != nil {
		return err
	}
	if err := c.t.c.GATTWrite(ctx, c.a, uint32(x.Handle), data, withResponse); err != nil {
		return espErr("write "+ch.String(), err)
	}
	return nil
}

func (c *espConn) Subscribe(ctx context.Context, ch UUID, fn func([]byte)) error {
	x, err := c.lookup("subscribe", ch)
	if err != nil {
		return err
	}
	if x.Props&(PropNotify|PropIndicate) == 0 {
		return fmt.Errorf("subscribe %s: characteristic cannot notify or indicate", ch)
	}
	// The proxy writes the CCCD itself. Values reach fn from a dedicated
	// goroutine so a slow callback never stalls the API read loop.
	q := newDropQueue[[]byte](userQueueLen)
	err = c.t.c.GATTNotify(ctx, c.a, uint32(x.Handle), func(b []byte) { q.push(append([]byte(nil), b...)) })
	if err != nil {
		return espErr("subscribe "+ch.String(), err)
	}
	go q.run(c.done, fn)
	return nil
}
