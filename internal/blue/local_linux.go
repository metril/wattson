//go:build linux

package blue

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

const (
	// localConnectScan bounds how long Connect scans for the target, because
	// BlueZ must know a device before it can be connected.
	localConnectScan = 10 * time.Second
	// localPollInterval is the link-liveness polling period; the BlueZ central
	// API has no remote-disconnect callback.
	localPollInterval = time.Second
	// localScanRestarts bounds scan restarts after a recovered tinygo panic.
	localScanRestarts = 5

	// metaTimeout bounds the D-Bus metadata read; pollTimeout one liveness poll.
	metaTimeout = 5 * time.Second
	pollTimeout = 2 * time.Second

	accessHint = "add the container user to the host bluetooth group (see docs/hardware-runbook.md)"
)

// NewLocal returns a Transport over the host's BlueZ adapter (hci0) via
// tinygo.org/x/bluetooth (pure Go, D-Bus).
func NewLocal() (Transport, error) {
	if _, err := guard(func() (struct{}, error) {
		return struct{}{}, bluetooth.DefaultAdapter.Enable()
	}); err != nil {
		return nil, localErr("enable bluetooth adapter", err)
	}
	return &localTransport{ad: bluetooth.DefaultAdapter, sem: make(chan struct{}, 1), conns: map[*localConn]struct{}{}}, nil
}

type localTransport struct {
	ad  *bluetooth.Adapter
	sem chan struct{} // serializes scans: tinygo allows one at a time and Connect also scans

	mu     sync.Mutex
	closed bool
	conns  map[*localConn]struct{}
}

// guard runs fn, converting a panic from the tinygo/BlueZ layer into an error.
func guard[T any](fn func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bluetooth stack panic: %v", r)
		}
	}()
	return fn()
}

// runCtx runs fn on its own goroutine and returns early if ctx ends first.
// The D-Bus calls underneath take no context; an abandoned call finishes in
// the background and discard (if set) cleans up its result.
func runCtx[T any](ctx context.Context, fn func() (T, error), discard func(T)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := guard(fn)
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		if discard != nil {
			go func() {
				if r := <-ch; r.err == nil {
					discard(r.v)
				}
			}()
		}
		var zero T
		return zero, ctx.Err()
	}
}

// localErr annotates err with op, adding the access hint for D-Bus permission
// failures and mapping lost-device errors to ErrDisconnected.
func localErr(op string, err error) error {
	var de dbus.Error
	name := ""
	if errors.As(err, &de) {
		name = de.Name
	}
	low := strings.ToLower(err.Error())
	switch {
	case name == "org.freedesktop.DBus.Error.AccessDenied" || name == "org.bluez.Error.NotAuthorized" ||
		strings.Contains(low, "access denied") || strings.Contains(low, "permission denied"):
		return fmt.Errorf("%s: %w: %s", op, err, accessHint)
	case name == "org.bluez.Error.NotConnected" || name == "org.freedesktop.DBus.Error.UnknownObject" ||
		strings.Contains(low, "not connected"):
		return fmt.Errorf("%s: %w (%v)", op, ErrDisconnected, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// tinygo stores MACs little-endian (index 0 is the last octet).
func toBT(a Address) bluetooth.Address {
	var m bluetooth.MAC
	for i := range m {
		m[i] = a.MAC[5-i]
	}
	return bluetooth.Address{MACAddress: bluetooth.NewMACAddress(m, a.Type == AddrRandom)}
}

func fromBT(a bluetooth.Address) Address {
	var out Address
	for i := range a.MAC {
		out.MAC[5-i] = a.MAC[i]
	}
	if a.IsRandom() {
		out.Type = AddrRandom
	}
	return out
}

func uuidFromBT(u bluetooth.UUID) UUID { return UUID(u.BytesBigEndian()) }

// propsFromFlags maps BlueZ GattCharacteristic1 "Flags" onto Props.
func propsFromFlags(flags []string) Props {
	var p Props
	for _, f := range flags {
		switch f {
		case "read":
			p |= PropRead
		case "write-without-response":
			p |= PropWriteNoResp
		case "write":
			p |= PropWrite
		case "notify":
			p |= PropNotify
		case "indicate":
			p |= PropIndicate
		}
	}
	return p
}

func advFromBT(r bluetooth.ScanResult) Advertisement {
	adv := Advertisement{Addr: fromBT(r.Address), Name: r.LocalName(), RSSI: int(r.RSSI)}
	for _, u := range r.ServiceUUIDs() {
		adv.Services = append(adv.Services, uuidFromBT(u))
	}
	return adv
}

// stopScan ends the in-progress tinygo scan. tinygo's StopScan checks and
// closes its cancel channel without locking, and the channel is also closed by
// tinygo itself (power-off, discovery stopped) and by our other stop paths, so
// a "close of closed channel" panic means the scan is already stopped.
func (t *localTransport) stopScan() (stopped bool) {
	defer func() {
		if recover() != nil {
			stopped = true
		}
	}()
	return t.ad.StopScan() == nil
}

// stopDiscovery asks BlueZ to stop discovering, best effort and bounded. It
// is needed after a recovered tinygo panic, which skips tinygo's own
// StopDiscovery.
func stopDiscovery() {
	bus, err := dbus.SystemBus()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	_ = bus.Object("org.bluez", "/org/bluez/hci0").CallWithContext(ctx, "org.bluez.Adapter1.StopDiscovery", 0).Err
}

// scan runs one BlueZ discovery at a time (waiting for the slot honours ctx),
// delivering results to cb on the scanning goroutine. cb may call
// t.ad.StopScan to end the scan. It returns once the scan has ended; ctx
// cancellation is not an error. tinygo's makeScanResult panics on devices
// whose properties lack UUIDs; such panics reset the adapter state and
// restart the scan.
func (t *localTransport) scan(ctx context.Context, cb func(bluetooth.ScanResult)) error {
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() { // StopScan fails until Scan has registered itself, so retry
		defer close(stopped)
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		tk := time.NewTicker(10 * time.Millisecond)
		defer tk.Stop()
		for {
			if t.stopScan() {
				return
			}
			select {
			case <-done:
				return
			case <-tk.C:
			}
		}
	}()
	defer func() { close(done); <-stopped }()

	var err error
	for attempt := 0; ; attempt++ {
		panicked := false
		err = func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					t.stopScan()    // clear tinygo's stuck "scanning" state
					stopDiscovery() // tinygo's StopDiscovery was skipped by the panic
					err = fmt.Errorf("bluetooth stack panic: %v", r)
				}
			}()
			return t.ad.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) { cb(r) })
		}()
		if panicked && ctx.Err() == nil && attempt < localScanRestarts {
			continue
		}
		break
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (t *localTransport) Scan(ctx context.Context, fn func(Advertisement)) error {
	q := newDropQueue[Advertisement](scanQueueLen)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		q.run(stop, fn)
	}()
	err := t.scan(ctx, func(r bluetooth.ScanResult) { q.push(advFromBT(r)) })
	// The scan slot is free again, so a callback blocked in Connect can finish.
	close(stop)
	wg.Wait()
	if err != nil {
		return localErr("scan", err)
	}
	return nil
}

// discover scans until BlueZ has seen addr (it must know the device first).
func (t *localTransport) discover(ctx context.Context, addr Address) error {
	sctx, cancel := context.WithTimeout(ctx, localConnectScan)
	defer cancel()
	want := toBT(addr).MAC
	found := false
	err := t.scan(sctx, func(r bluetooth.ScanResult) {
		if !found && r.Address.MAC == want {
			found = true
			t.stopScan()
		}
	})
	switch {
	case found:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return localErr("scan for "+addr.String(), err)
	}
	return fmt.Errorf("%s not seen within %s: %w", addr, localConnectScan, ErrNotFound)
}

func (t *localTransport) Connect(ctx context.Context, addr Address) (Conn, error) {
	op := "connect " + addr.String()
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("%s: transport closed", op)
	}
	if err := t.discover(ctx, addr); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	dev, err := runCtx(ctx, func() (bluetooth.Device, error) {
		return t.ad.Connect(toBT(addr), bluetooth.ConnectionParams{})
	}, func(d bluetooth.Device) { _ = d.Disconnect() })
	if err != nil {
		return nil, localErr(op, err)
	}
	c := &localConn{t: t, addr: addr, dev: dev, done: make(chan struct{}), chars: map[UUID]*localChar{}}
	// Discovery can panic (tinygo #278, reconnect right after Disconnect);
	// guard turns that into an error.
	if err := c.discover(ctx); err != nil {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_, _ = runCtx(dctx, func() (struct{}, error) { return struct{}{}, dev.Disconnect() }, nil)
		return nil, localErr(op+": discover services", err)
	}
	wctx, wcancel := context.WithCancel(context.Background())
	c.stopWatch = wcancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		watchLink(wctx, localPollInterval, func() (bool, error) {
			// Bounded and cancelled by Close, so Close never waits on a
			// hung D-Bus poll.
			pctx, pcancel := context.WithTimeout(wctx, pollTimeout)
			defer pcancel()
			ok, err := runCtx(pctx, dev.Connected, nil)
			if errors.Is(err, context.DeadlineExceeded) && wctx.Err() == nil {
				return true, nil // slow poll is inconclusive, not a drop
			}
			return ok, err
		}, func() {
			c.finish(fmt.Errorf("link to %s lost: %w", addr, ErrDisconnected))
		})
	}()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = c.Close()
		return nil, fmt.Errorf("%s: transport closed", op)
	}
	t.conns[c] = struct{}{}
	t.mu.Unlock()
	return c, nil
}

// Close ends every open connection. The shared adapter is left enabled.
func (t *localTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	conns := make([]*localConn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return nil
}

type localChar struct {
	ch   bluetooth.DeviceCharacteristic
	info Characteristic
	subd bool
}

type localConn struct {
	t    *localTransport
	addr Address
	dev  bluetooth.Device

	services  []Service
	chars     map[UUID]*localChar
	stopWatch context.CancelFunc
	wg        sync.WaitGroup // link watcher

	done chan struct{}
	once sync.Once
	mu   sync.Mutex // guards err and localChar.subd
	err  error

	closeOnce sync.Once
}

func (c *localConn) Services() []Service   { return c.services }
func (c *localConn) Done() <-chan struct{} { return c.done }

func (c *localConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// finish ends the connection once; it reports whether this call did so.
func (c *localConn) finish(err error) (first bool) {
	c.once.Do(func() {
		first = true
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
	return first
}

type charMeta struct {
	uuid   UUID
	handle uint16
	props  Props
}

// gattMeta reads characteristic Flags and Handle straight from BlueZ, since
// tinygo does not expose them on Linux. It returns, per de-duplicated service
// (in path order, the order tinygo discovers them), the service UUID and its
// characteristics in path order. Failure just means Props stay unknown.
func (c *localConn) gattMeta(ctx context.Context) (svcUUIDs []UUID, chars [][]charMeta) {
	bus, err := dbus.SystemBus() // shared connection, same as tinygo's
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, metaTimeout)
	defer cancel()
	var list map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := bus.Object("org.bluez", "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&list); err != nil {
		return nil, nil
	}
	devPath := "/org/bluez/hci0/dev_" + strings.ReplaceAll(c.addr.String()[:17], ":", "_")
	paths := make([]string, 0, len(list))
	for p := range list {
		if strings.HasPrefix(string(p), devPath+"/service") {
			paths = append(paths, string(p))
		}
	}
	sort.Strings(paths)
	seen := map[UUID]bool{}
	for _, sp := range paths {
		sprops, ok := list[dbus.ObjectPath(sp)]["org.bluez.GattService1"]
		if !ok {
			continue
		}
		s, _ := sprops["UUID"].Value().(string)
		su, err := ParseUUID(s)
		if err != nil || seen[su] {
			continue
		}
		seen[su] = true
		var metas []charMeta
		for _, cp := range paths {
			if !strings.HasPrefix(cp, sp+"/char") {
				continue
			}
			cprops, ok := list[dbus.ObjectPath(cp)]["org.bluez.GattCharacteristic1"]
			if !ok {
				continue
			}
			cu, err := ParseUUID(func() string { s, _ := cprops["UUID"].Value().(string); return s }())
			if err != nil {
				continue
			}
			m := charMeta{uuid: cu}
			if f, ok := cprops["Flags"].Value().([]string); ok {
				m.props = propsFromFlags(f)
			}
			m.handle, _ = cprops["Handle"].Value().(uint16)
			metas = append(metas, m)
		}
		svcUUIDs = append(svcUUIDs, su)
		chars = append(chars, metas)
	}
	return svcUUIDs, chars
}

func (c *localConn) discover(ctx context.Context) error {
	type found struct {
		uuid  UUID
		chars []bluetooth.DeviceCharacteristic
	}
	fs, err := runCtx(ctx, func() ([]found, error) {
		svcs, err := c.dev.DiscoverServices(nil)
		if err != nil {
			return nil, err
		}
		var out []found
		for _, s := range svcs {
			chs, err := s.DiscoverCharacteristics(nil)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", s.UUID(), err)
			}
			out = append(out, found{uuidFromBT(s.UUID()), chs})
		}
		return out, nil
	}, nil)
	if err != nil {
		return err
	}
	metaSvc, metaChars := c.gattMeta(ctx)
	for i, f := range fs {
		// Flags/Handle pair up by position; trust them only if UUIDs agree.
		var metas []charMeta
		if i < len(metaSvc) && metaSvc[i] == f.uuid && len(metaChars[i]) == len(f.chars) {
			metas = metaChars[i]
		}
		sv := Service{UUID: f.uuid}
		for j, ch := range f.chars {
			x := Characteristic{UUID: uuidFromBT(ch.UUID())}
			if metas != nil && metas[j].uuid == x.UUID {
				x.Props, x.Handle = metas[j].props, metas[j].handle
			}
			sv.Chars = append(sv.Chars, x)
			if _, dup := c.chars[x.UUID]; !dup {
				c.chars[x.UUID] = &localChar{ch: ch, info: x}
			}
		}
		c.services = append(c.services, sv)
	}
	return nil
}

// lookup checks the link is up and resolves ch.
func (c *localConn) lookup(op string, ch UUID) (*localChar, error) {
	select {
	case <-c.done:
		return nil, fmt.Errorf("%s %s: connection closed: %w", op, ch, ErrDisconnected)
	default:
	}
	x, ok := c.chars[ch]
	if !ok {
		return nil, fmt.Errorf("%s %s: %w", op, ch, ErrNoCharacteristic)
	}
	return x, nil
}

func (c *localConn) Read(ctx context.Context, ch UUID) ([]byte, error) {
	x, err := c.lookup("read", ch)
	if err != nil {
		return nil, err
	}
	b, err := runCtx(ctx, func() ([]byte, error) {
		buf := make([]byte, 512)
		n, err := x.ch.Read(buf)
		return buf[:n], err
	}, nil)
	if err != nil {
		return nil, localErr("read "+ch.String(), err)
	}
	return b, nil
}

func (c *localConn) Write(ctx context.Context, ch UUID, data []byte, withResponse bool) error {
	x, err := c.lookup("write", ch)
	if err != nil {
		return err
	}
	data = append([]byte(nil), data...)
	_, err = runCtx(ctx, func() (int, error) {
		if withResponse {
			return x.ch.Write(data)
		}
		return x.ch.WriteWithoutResponse(data)
	}, nil)
	if err != nil {
		return localErr("write "+ch.String(), err)
	}
	return nil
}

func (c *localConn) Subscribe(ctx context.Context, ch UUID, fn func([]byte)) error {
	x, err := c.lookup("subscribe", ch)
	if err != nil {
		return err
	}
	// Props are zero when BlueZ metadata was unavailable; then just try.
	if p := x.info.Props; p != 0 && p&(PropNotify|PropIndicate) == 0 {
		return fmt.Errorf("subscribe %s: characteristic cannot notify or indicate", ch)
	}
	c.mu.Lock()
	already := x.subd
	x.subd = true
	c.mu.Unlock()
	if already {
		return fmt.Errorf("subscribe %s: already subscribed", ch)
	}
	// Values reach fn from a dedicated goroutine so a slow callback never
	// stalls tinygo's D-Bus signal loop.
	q := newDropQueue[[]byte](userQueueLen)
	_, err = runCtx(ctx, func() (struct{}, error) {
		return struct{}{}, x.ch.EnableNotifications(func(b []byte) { q.push(append([]byte(nil), b...)) })
	}, func(struct{}) { // ctx ended first but the call later succeeded
		_, _ = guard(func() (struct{}, error) { return struct{}{}, x.ch.EnableNotifications(nil) })
		c.mu.Lock()
		x.subd = false
		c.mu.Unlock()
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { // abandoned: discard cleans up
			c.mu.Lock()
			x.subd = false
			c.mu.Unlock()
			return localErr("subscribe "+ch.String(), err)
		}
		// tinygo leaves its signal registration behind on StartNotify failure.
		_, _ = guard(func() (struct{}, error) { return struct{}{}, x.ch.EnableNotifications(nil) })
		c.mu.Lock()
		x.subd = false
		c.mu.Unlock()
		return localErr("subscribe "+ch.String(), err)
	}
	go q.run(c.done, fn)
	return nil
}

func (c *localConn) Close() error {
	c.finish(nil)
	c.closeOnce.Do(func() {
		if c.stopWatch != nil {
			c.stopWatch()
		}
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		// Release notification goroutines/signal registrations (tinygo #260),
		// then drop the link; both are best effort on a possibly dead link.
		for _, x := range c.chars {
			c.mu.Lock()
			subd := x.subd
			c.mu.Unlock()
			if subd {
				_, _ = runCtx(ctx, func() (struct{}, error) { return struct{}{}, x.ch.EnableNotifications(nil) }, nil)
			}
		}
		_, _ = runCtx(ctx, func() (struct{}, error) { return struct{}{}, c.dev.Disconnect() }, nil)
	})
	c.wg.Wait()
	c.t.mu.Lock()
	delete(c.t.conns, c)
	c.t.mu.Unlock()
	return nil
}
