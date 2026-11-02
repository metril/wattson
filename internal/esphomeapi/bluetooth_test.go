package esphomeapi

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

const (
	devA uint64 = 0x010203040506
	devB uint64 = 0x0a0b0c0d0e0f
)

func bleServer(t *testing.T) *esphometest.Server {
	t.Helper()
	srv := newServer(t, 0x3f)
	svcs := []esphometest.GATTService{{UUID: "fff0", Handle: 1, Chars: []esphometest.GATTChar{
		{UUID: "fff1", Handle: 3, Props: PropNotify},
		{UUID: "fff2", Handle: 6, Props: PropWrite | PropWriteNoResp},
		{UUID: "2a00", Handle: 8, Props: PropRead},
	}}}
	srv.AddBLEDevice(esphometest.RawAdv{Address: devA, RSSI: -60, AddressType: 1, Data: []byte{2, 1, 6}}, svcs)
	srv.AddBLEDevice(esphometest.RawAdv{Address: devB, RSSI: -70, Data: []byte{2, 1, 6}}, svcs)
	srv.SetBLEValue(devA, 8, []byte("MRK"))
	return srv
}

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// bleClient dials and holds an advertisement subscription for the test: the
// proxy sends every BLE reply only to the subscribed client.
func bleClient(t *testing.T, srv *esphometest.Server) *Client {
	t.Helper()
	c := dialOpts(t, srv, DialOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := c.SubscribeAdvertisements(ctx, func(RawAdv) {}); err != nil {
		t.Fatalf("SubscribeAdvertisements: %v", err)
	}
	return c
}

func connected(t *testing.T, c *Client, addr uint64) {
	t.Helper()
	if _, err := c.BLEConnect(tctx(t), addr, 1); err != nil {
		t.Fatalf("BLEConnect: %v", err)
	}
}

func TestSubscribeAdvertisementsRawAndUnsubscribe(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := dialOpts(t, srv, DialOptions{})
	ctx, cancel := context.WithCancel(tctx(t))
	got := make(chan RawAdv, 8)
	if err := c.SubscribeAdvertisements(ctx, func(a RawAdv) {
		select {
		case got <- a:
		default:
		}
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[uint64]RawAdv{}
	for len(seen) < 2 {
		select {
		case a := <-got:
			seen[a.Address] = a
		case <-ctx.Done():
			t.Fatal("no advertisements")
		}
	}
	if a := seen[devA]; a.RSSI != -60 || a.AddressType != 1 || !bytes.Equal(a.Data, []byte{2, 1, 6}) {
		t.Fatalf("adv = %+v", a)
	}
	if f := srv.BLEAdvFlags(); len(f) != 1 || f[0] != 1 {
		t.Fatalf("flags = %v, want [1] (raw)", f)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for srv.BLEUnsubscribes() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no Unsubscribe after ctx cancel")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBLEConnectServicesWriteReadNotifyDisconnect(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	ctx := tctx(t)

	mtu, err := c.BLEConnect(ctx, devA, 1)
	if err != nil || mtu != 247 {
		t.Fatalf("BLEConnect = %d, %v", mtu, err)
	}
	reqs := srv.BLEConnRequests()
	if len(reqs) != 1 || reqs[0].RequestType != 4 || !reqs[0].HasAddressType || reqs[0].AddressType != 1 || reqs[0].Address != devA {
		t.Fatalf("requests = %+v", reqs)
	}
	svcs, err := c.GATTServices(ctx, devA)
	if err != nil || len(svcs) != 1 || len(svcs[0].Characteristics) != 3 {
		t.Fatalf("GATTServices = %+v, %v", svcs, err)
	}
	if got := svcs[0].Characteristics[0].Full(); got != shortToFull(0xfff1) {
		t.Fatalf("uuid = %v", got)
	}
	if err := c.GATTWrite(ctx, devA, 6, []byte{1, 2}, true); err != nil {
		t.Fatalf("write resp: %v", err)
	}
	if err := c.GATTWrite(ctx, devA, 6, []byte{3}, false); err != nil {
		t.Fatalf("write noresp: %v", err)
	}
	// The no-response write has no ack; wait for the server to see it.
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.BLEWrites(devA)) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w := srv.BLEWrites(devA); len(w) != 2 || !bytes.Equal(w[0], []byte{1, 2}) || !bytes.Equal(w[1], []byte{3}) {
		t.Fatalf("writes = %x", w)
	}
	if v, err := c.GATTRead(ctx, devA, 8); err != nil || string(v) != "MRK" {
		t.Fatalf("GATTRead = %q, %v", v, err)
	}
	got := make(chan []byte, 1)
	if err := c.GATTNotify(ctx, devA, 3, func(b []byte) { got <- append([]byte(nil), b...) }); err != nil {
		t.Fatalf("GATTNotify: %v", err)
	}
	srv.NotifyBLE(devA, 3, []byte{9, 8})
	if b := <-got; !bytes.Equal(b, []byte{9, 8}) {
		t.Fatalf("notify = %x", b)
	}
	// The proxy writes the CCCD itself: the client must send no GATT write for it.
	if w := srv.BLEWrites(devA); len(w) != 2 {
		t.Fatalf("extra writes after notify enable: %x", w)
	}
	if err := c.BLEDisconnect(ctx, devA); err != nil {
		t.Fatalf("BLEDisconnect: %v", err)
	}
	if srv.BLEConnected(devA) {
		t.Fatal("still connected")
	}
}

func TestBLEConnectNoSlots(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	srv.SetFreeSlots(0)
	c := bleClient(t, srv)
	if _, err := c.BLEConnect(tctx(t), devA, 0); !errors.Is(err, ErrNoSlots) {
		t.Fatalf("err = %v, want ErrNoSlots", err)
	}
	if n := len(srv.BLEConnRequests()); n != 0 {
		t.Fatalf("%d connect requests sent despite no slots", n)
	}
}

func TestBLEConnectUnknownHonoursContext(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.BLEConnect(ctx, 0xdead, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("took %v", el)
	}
}

func TestGATTErrorMapped(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	err := c.GATTWrite(tctx(t), devA, 99, []byte{1}, true)
	var ge *GATTError
	if !errors.As(err, &ge) || ge.Handle != 99 || ge.Code != esphometest.ErrCodeGATT {
		t.Fatalf("err = %v, want *GATTError{handle 99}", err)
	}
}

func TestOnBLEDisconnectLinkLoss(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	connected(t, c, devB)
	gotA, gotB := make(chan error, 1), make(chan error, 1)
	c.OnBLEDisconnect(devA, func(err error) { gotA <- err })
	c.OnBLEDisconnect(devB, func(err error) { gotB <- err })
	srv.DropBLE(devA)
	select {
	case err := <-gotA:
		if !errors.Is(err, ErrBLEDisconnected) {
			t.Fatalf("err = %v", err)
		}
	case <-tctx(t).Done():
		t.Fatal("no disconnect callback")
	}
	select {
	case <-gotB:
		t.Fatal("device B callback fired for A's drop")
	default:
	}
}

func TestOpsOnNotConnectedDeviceReportGATTNotConnected(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	ctx := tctx(t)
	// Real bluetooth_proxy replies GATT error -1: handle 0 for get-services,
	// the request handle for read/write/notify.
	check := func(name string, err error, handle uint32) {
		t.Helper()
		var ge *GATTError
		if !errors.As(err, &ge) || ge.Code != GATTErrNotConnected || ge.Handle != handle || ge.Address != devA {
			t.Errorf("%s: err = %v, want GATTError{-1, handle %d}", name, err, handle)
		}
	}
	_, err := c.GATTServices(ctx, devA)
	check("services", err, 0)
	_, err = c.GATTRead(ctx, devA, 8)
	check("read", err, 8)
	check("write", c.GATTWrite(ctx, devA, 6, []byte{1}, true), 6)
	check("notify", c.GATTNotify(ctx, devA, 3, func([]byte) {}), 3)
}

func TestOpAfterDropFailsPromptly(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	srv.DropBLE(devA)
	start := time.Now()
	_, err := c.GATTRead(tctx(t), devA, 8)
	var ge *GATTError
	if !errors.Is(err, ErrBLEDisconnected) && !errors.As(err, &ge) {
		t.Fatalf("err = %v, want a disconnect error", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %v", el)
	}
}

func TestOversizeMessageDoesNotCloseClient(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	if err := c.GATTWrite(tctx(t), devA, 6, make([]byte, 70000), false); !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want errTooLarge", err)
	}
	select {
	case <-c.Done():
		t.Fatalf("client closed by a pre-write rejection: %v", c.Err())
	default:
	}
	if err := c.GATTWrite(tctx(t), devA, 6, []byte{1}, true); err != nil {
		t.Fatalf("write after oversize: %v", err)
	}
}

func TestNotifyFilteredByAddress(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	connected(t, c, devB)
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	ctx := tctx(t)
	if err := c.GATTNotify(ctx, devA, 3, func(p []byte) { a <- append([]byte(nil), p...) }); err != nil {
		t.Fatal(err)
	}
	if err := c.GATTNotify(ctx, devB, 3, func(p []byte) { b <- append([]byte(nil), p...) }); err != nil {
		t.Fatal(err)
	}
	srv.NotifyBLE(devB, 3, []byte{0xb})
	srv.NotifyBLE(devA, 3, []byte{0xa})
	if p := <-a; !bytes.Equal(p, []byte{0xa}) {
		t.Fatalf("a = %x", p)
	}
	if p := <-b; !bytes.Equal(p, []byte{0xb}) {
		t.Fatalf("b = %x", p)
	}
	if len(a)+len(b) != 0 {
		t.Fatal("cross-delivery")
	}
}

func TestNotifyHandlerRemovedAfterDisconnect(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	ctx := tctx(t)
	connected(t, c, devA)
	old := make(chan []byte, 4)
	if err := c.GATTNotify(ctx, devA, 3, func(p []byte) { old <- p }); err != nil {
		t.Fatal(err)
	}
	if err := c.BLEDisconnect(ctx, devA); err != nil {
		t.Fatal(err)
	}
	connected(t, c, devA)
	fresh := make(chan []byte, 4)
	if err := c.GATTNotify(ctx, devA, 3, func(p []byte) { fresh <- p }); err != nil {
		t.Fatal(err)
	}
	srv.NotifyBLE(devA, 3, []byte{1})
	<-fresh
	if len(old) != 0 {
		t.Fatal("stale notify handler from previous connection still called")
	}
}

func TestConcurrentWritesSameHandleDoNotCross(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	ctx := tctx(t)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { errs <- c.GATTWrite(ctx, devA, 6, []byte{1}, true) }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := len(srv.BLEWrites(devA)); n != 8 {
		t.Fatalf("writes = %d", n)
	}
}

func TestOpsOnClosedClient(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := bleClient(t, srv)
	connected(t, c, devA)
	srv.CloseClient()
	waitDone(t, c, 2*time.Second)
	if err := c.GATTWrite(tctx(t), devA, 6, []byte{1}, true); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

// The proxy replies to BLE requests only for the advertisement subscriber.
func TestProxyDropsBLERepliesWithoutSubscription(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := dialOpts(t, srv, DialOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.BLEConnect(ctx, devA, 1); err == nil {
		t.Fatal("BLEConnect succeeded without an advertisement subscription")
	}
	if n := len(srv.BLEConnRequests()); n == 0 { // the client may re-send while waiting
		t.Fatal("proxy saw no connect request")
	}
	if srv.BLEConnected(devA) {
		t.Fatal("proxy connected a device for an unsubscribed client")
	}
	if srv.BLEDroppedReplies() == 0 {
		t.Fatal("proxy did not record dropped replies")
	}
	// Subscribing makes the same request work.
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	if err := c.SubscribeAdvertisements(sctx, func(RawAdv) {}); err != nil {
		t.Fatal(err)
	}
	connected(t, c, devA)
}

// After Unsubscribe the proxy drops replies again.
func TestProxyDropsBLERepliesAfterUnsubscribe(t *testing.T) {
	t.Parallel()
	srv := bleServer(t)
	c := dialOpts(t, srv, DialOptions{})
	sctx, scancel := context.WithCancel(context.Background())
	if err := c.SubscribeAdvertisements(sctx, func(RawAdv) {}); err != nil {
		t.Fatal(err)
	}
	connected(t, c, devA)
	scancel()
	for srv.BLEUnsubscribes() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.GATTServices(ctx, devA); err == nil {
		t.Fatal("GATTServices answered after Unsubscribe")
	}
}
