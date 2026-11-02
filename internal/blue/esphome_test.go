package blue_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

var (
	espNotify = blue.UUID16(0xfff1)
	espWrite  = blue.UUID16(0xfff2)
	espRead   = blue.UUID16(0x2a00)
	espDev    = blue.Address{MAC: [6]byte{1, 2, 3, 4, 5, 6}, Type: blue.AddrRandom}
)

const (
	hNotify = 3
	hWrite  = 6
	hRead   = 8
)

type espEnv struct {
	srv *esphometest.Server
	tr  blue.Transport
	dev blue.Address
}

func newESPEnv(t *testing.T, dev blue.Address) espEnv {
	t.Helper()
	return newESPEnvOpts(t, dev, blue.WithProbeTimeout(100*time.Millisecond))
}

func newESPEnvOpts(t *testing.T, dev blue.Address, opts ...blue.ESPHomeOption) espEnv {
	t.Helper()
	k := bytes.Repeat([]byte{7}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	data := []byte{10, 0x09}
	data = append(data, "MRK-S29R2"...)
	data = append(data, 3, 0x03, 0xf0, 0xff)
	srv.AddBLEDevice(esphometest.RawAdv{Address: dev.Uint64(), RSSI: -61, AddressType: uint32(dev.Type), Data: data},
		[]esphometest.GATTService{{UUID: "fff0", Handle: 1, Chars: []esphometest.GATTChar{
			{UUID: "fff1", Handle: hNotify, Props: uint32(blue.PropNotify)},
			{UUID: "fff2", Handle: hWrite, Props: uint32(blue.PropWrite | blue.PropWriteNoResp)},
			{UUID: "2a00", Handle: hRead, Props: uint32(blue.PropRead)},
		}}})
	srv.SetBLEValue(dev.Uint64(), hRead, []byte("MRK"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tr, err := blue.NewESPHomeWith(ctx, srv.Addr(), base64.StdEncoding.EncodeToString(k), opts...)
	if err != nil {
		t.Fatalf("NewESPHome: %v", err)
	}
	t.Cleanup(func() { tr.Close() })
	return espEnv{srv, tr, dev}
}

func TestESPHomeContract(t *testing.T) {
	t.Parallel()
	for name, typ := range map[string]blue.AddrType{"random": blue.AddrRandom, "public": blue.AddrPublic} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := espDev
			dev.Type = typ
			bluetest.RunContract(t, func(t *testing.T) bluetest.Harness {
				e := newESPEnv(t, dev)
				return bluetest.Harness{
					Transport: e.tr, Dev: dev,
					NotifyChar: espNotify, WriteChar: espWrite, ReadChar: espRead, ReadValue: []byte("MRK"),
					Push:   func(b []byte) { e.srv.NotifyBLE(dev.Uint64(), hNotify, b) },
					Drop:   func() { e.srv.DropBLE(dev.Uint64()) },
					Writes: func() [][]byte { return e.srv.BLEWrites(dev.Uint64()) },
				}
			})
		})
	}
}

func ctx2s(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestESPHomeConnectSendsAddressType(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reqs := e.srv.BLEConnRequests()
	if len(reqs) == 0 {
		t.Fatal("no device request")
	}
	r := reqs[0]
	if r.RequestType != 4 || !r.HasAddressType || r.AddressType != 1 || r.Address != e.dev.Uint64() {
		t.Fatalf("request = %+v", r)
	}
}

func TestESPHomeNoSlots(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	e.srv.SetFreeSlots(0)
	if _, err := e.tr.Connect(ctx2s(t), e.dev); !errors.Is(err, blue.ErrNoSlots) {
		t.Fatalf("err = %v, want ErrNoSlots", err)
	}
}

func TestESPHomeProxyLinkLossClosesConn(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.CloseClient()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after proxy link loss")
	}
	if err := c.Err(); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("Err = %v", err)
	}
	if err := c.Write(ctx2s(t), espWrite, []byte{1}, true); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("Write = %v", err)
	}
}

func TestESPHomeSubscribeWritesNoCCCD(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	ctx := ctx2s(t)
	c, err := e.tr.Connect(ctx, e.dev)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx, espNotify, func([]byte) {}); err != nil {
		t.Fatal(err)
	}
	if w := e.srv.BLEWrites(e.dev.Uint64()); len(w) != 0 {
		t.Fatalf("client wrote %x; the proxy writes the CCCD itself", w)
	}
	if err := c.Subscribe(ctx, espWrite, func([]byte) {}); err == nil {
		t.Fatal("Subscribe on non-notify characteristic succeeded")
	}
}

// A blocked user callback must not stall the proxy read loop: other
// operations keep working and notifications are dropped oldest-first.
func TestESPHomeSlowSubscriberDoesNotStallClient(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	ctx := ctx2s(t)
	c, err := e.tr.Connect(ctx, e.dev)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	block := make(chan struct{})
	var calls atomic.Int32
	var latest atomic.Int32
	latest.Store(-1)
	if err := c.Subscribe(ctx, espNotify, func(b []byte) {
		if calls.Add(1) == 1 {
			<-block
		}
		latest.Store(int32(b[0]))
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ { // far more than the queue holds
		e.srv.NotifyBLE(e.dev.Uint64(), hNotify, []byte{byte(i)})
	}
	if err := c.Write(ctx, espWrite, []byte{1}, true); err != nil {
		t.Fatalf("Write while callback blocked: %v", err)
	}
	if v, err := c.Read(ctx, espRead); err != nil || string(v) != "MRK" {
		t.Fatalf("Read = %q, %v", v, err)
	}
	close(block)
	// Newest values survive (299 -> byte 43); the oldest were dropped.
	deadline := time.Now().Add(2 * time.Second)
	for latest.Load() != int32(byte(299%256)) {
		if time.Now().After(deadline) {
			t.Fatalf("newest notification never delivered (latest=%d)", latest.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if n := calls.Load(); n >= 300 {
		t.Fatalf("callback ran %d times; expected drops", n)
	}
}

func TestESPHomeScanCallbackOffReadGoroutine(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	entered := make(chan blue.Advertisement, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- e.tr.Scan(sctx, func(a blue.Advertisement) {
			select {
			case entered <- a:
			default:
			}
			<-release
		})
	}()
	a := <-entered
	if a.Name != "MRK-S29R2" || a.RSSI != -61 || a.Addr != e.dev || len(a.Services) != 1 || a.Services[0] != blue.UUID16(0xfff0) {
		t.Fatalf("adv = %+v", a)
	}
	// Callback is blocked; the client must still serve requests.
	c, err := e.tr.Connect(ctx, e.dev)
	if err != nil {
		t.Fatalf("Connect during blocked scan callback: %v", err)
	}
	c.Close()
	stop()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Scan = %v", err)
	}
}

func TestESPHomeTransportCloseEndsConnAndScan(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	ctx := ctx2s(t)
	c, err := e.tr.Connect(ctx, e.dev)
	if err != nil {
		t.Fatal(err)
	}
	scanErr := make(chan error, 1)
	go func() { scanErr <- e.tr.Scan(ctx, func(blue.Advertisement) {}) }()
	time.Sleep(20 * time.Millisecond)
	e.tr.Close()
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal("conn not closed by transport Close")
	}
	select {
	case err := <-scanErr:
		if err == nil {
			t.Fatal("Scan returned nil after transport Close")
		}
	case <-ctx.Done():
		t.Fatal("Scan did not exit")
	}
}

// The link drops after the connect succeeded but before service discovery
// completes: Connect must fail promptly with ErrDisconnected, not hang.
func TestESPHomeDropBetweenConnectAndServices(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	e.srv.OnGetServices(func() { e.srv.DropBLE(e.dev.Uint64()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	c, err := e.tr.Connect(ctx, e.dev)
	if err == nil {
		c.Close()
		t.Fatal("Connect succeeded despite drop")
	}
	if !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("err = %v, want ErrDisconnected", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("Connect took %v, want prompt failure", el)
	}
}

func TestESPHomeCloseAfterRemoteDrop(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.DropBLE(e.dev.Uint64())
	<-c.Done()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(c.Err(), blue.ErrDisconnected) {
		t.Fatalf("Err = %v", c.Err())
	}
}

// Connect works without any prior Scan only because the transport holds an
// advertisement subscription: the proxy sends every BLE reply to the
// subscriber alone.
func TestESPHomeConnectWithoutScanUsesLifetimeSubscription(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatalf("Connect without Scan: %v", err)
	}
	defer c.Close()
	if f := e.srv.BLEAdvFlags(); len(f) != 1 || f[0] != 1 {
		t.Fatalf("advert subscriptions = %v, want exactly one (flags=1)", f)
	}
	if n := e.srv.BLEDroppedReplies(); n != 0 {
		t.Fatalf("proxy dropped %d replies", n)
	}
}

// Scan must leave the subscription in place: Connect after the scan context
// ended still gets its replies, and Unsubscribe is sent only by Close.
func TestESPHomeScanThenConnectKeepsSubscription(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	sctx, stop := context.WithCancel(ctx2s(t))
	seen := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- e.tr.Scan(sctx, func(blue.Advertisement) {
			select {
			case seen <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-seen:
	case <-sctx.Done():
		t.Fatal("scan saw nothing")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Scan = %v", err)
	}
	if n := e.srv.BLEUnsubscribes(); n != 0 {
		t.Fatalf("Scan sent %d Unsubscribe(s); only Close may", n)
	}
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatalf("Connect after Scan: %v", err)
	}
	c.Close()
	if f := e.srv.BLEAdvFlags(); len(f) != 1 {
		t.Fatalf("advert subscriptions = %v, want one for the transport lifetime", f)
	}
	e.tr.Close()
	deadline := time.Now().Add(2 * time.Second)
	for e.srv.BLEUnsubscribes() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("transport Close did not unsubscribe")
		}
		time.Sleep(time.Millisecond)
	}
}

// espProxyEnv dials a transport to srv (already configured by the caller).
func espProxy(t *testing.T, srv *esphometest.Server, k []byte) blue.Transport {
	t.Helper()
	tr, err := blue.NewESPHome(ctx2s(t), srv.Addr(), base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatalf("NewESPHome: %v", err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

func proxyOf(t *testing.T, tr blue.Transport) blue.Proxy {
	t.Helper()
	p, ok := tr.(blue.Proxy)
	if !ok {
		t.Fatal("ESPHome transport does not implement blue.Proxy")
	}
	return p
}

func TestConnectionsFreeViaProxyStatus(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := proxyOf(t, e.tr).ProxyStatus(ctx2s(t))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Subscribed || st.Free != 2 || st.Limit != 3 || len(st.Allocated) != 1 || st.Allocated[0].MAC != e.dev.MAC { // the proxy does not report address types
		t.Fatalf("status = %+v", st)
	}
}

func TestProxyStatusSubscribed(t *testing.T) {
	t.Parallel()
	k := bytes.Repeat([]byte{7}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	st, err := proxyOf(t, espProxy(t, srv, k)).ProxyStatus(ctx2s(t))
	if err != nil {
		t.Fatal(err)
	}
	want := blue.ProxyStatus{Name: esphometest.Name, MAC: esphometest.MAC, Version: "2026.10.1", Flags: 0x3f, Subscribed: true, Free: 3, Limit: 3}
	if st.Name != want.Name || st.MAC != want.MAC || st.Version != want.Version || st.Flags != want.Flags ||
		!st.Subscribed || st.Free != 3 || st.Limit != 3 || len(st.Allocated) != 0 {
		t.Fatalf("status = %+v, want %+v", st, want)
	}
}

func TestProxyStatusForeign(t *testing.T) {
	t.Parallel()
	k := bytes.Repeat([]byte{7}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	srv.SetForeignSubscriber()
	p := proxyOf(t, espProxy(t, srv, k))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	st, err := p.ProxyStatus(ctx)
	if err != nil || st.Subscribed || st.Name != esphometest.Name {
		t.Fatalf("status = %+v, err %v; want Subscribed false and nil error", st, err)
	}
}

func TestProxyStatusLinkDown(t *testing.T) {
	t.Parallel()
	k := bytes.Repeat([]byte{7}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	p := proxyOf(t, espProxy(t, srv, k))
	srv.CloseClient()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after the API link dropped")
	}
	if _, err := p.ProxyStatus(ctx2s(t)); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("err = %v, want ErrDisconnected", err)
	}
}

// Another client taking the subscription silences the proxy: Connect says so
// instead of waiting out its timeout, and does not try to take it back.
func TestESPHomeConnectProxyHeld(t *testing.T) {
	t.Parallel()
	e := newESPEnv(t, espDev)
	c, err := e.tr.Connect(ctx2s(t), e.dev)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	e.srv.SetForeignSubscriber() // newest subscriber wins: we just lost it
	_, err = e.tr.Connect(ctx2s(t), e.dev)
	if !errors.Is(err, blue.ErrProxyHeld) {
		t.Fatalf("err = %v, want ErrProxyHeld", err)
	}
	if !strings.Contains(err.Error(), "another client") {
		t.Fatalf("err %q carries no hint", err)
	}
	if f := e.srv.BLEAdvFlags(); len(f) != 1 {
		t.Fatalf("advert subscriptions = %v: Wattson must not re-subscribe", f)
	}
}
