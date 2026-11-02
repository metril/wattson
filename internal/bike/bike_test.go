package bike

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var testAddr = blue.Address{MAC: [6]byte{1, 2, 3, 4, 5, 6}}

func fitPlusDevice() *bluetest.Device {
	return &bluetest.Device{
		Adv: blue.Advertisement{Addr: testAddr, Name: "fp"},
		Services: []blue.Service{{UUID: FitPlusService, Chars: []blue.Characteristic{
			{UUID: FitPlusNotify, Props: blue.PropNotify},
			{UUID: FitPlusWrite, Props: blue.PropWriteNoResp},
		}}},
	}
}

func ftmsDevice(rng []byte) *bluetest.Device {
	d := &bluetest.Device{
		Adv: blue.Advertisement{Addr: testAddr, Name: "ftms"},
		Services: []blue.Service{{UUID: FTMSService, Chars: []blue.Characteristic{
			{UUID: FTMSIndoorBikeData, Props: blue.PropNotify},
			{UUID: FTMSControlPoint, Props: blue.PropWrite | blue.PropIndicate},
			{UUID: FTMSResistanceRange, Props: blue.PropRead},
		}}},
		Values: map[blue.UUID][]byte{},
	}
	if rng != nil {
		d.Values[FTMSResistanceRange] = rng
	}
	return d
}

func connect(t *testing.T, d *bluetest.Device) (*bluetest.Fake, blue.Conn) {
	t.Helper()
	f := bluetest.NewFake(d)
	c, err := f.Connect(context.Background(), testAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = f.Close() })
	return f, c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitWithin(t, 2*time.Second, what, cond)
}

func waitWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func lvl(t *testing.T, l int) []byte {
	t.Helper()
	b, err := FitPlusSetLevel(l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenFitPlusSendsInitThenKeepalive(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if b.Protocol() != ProtoFitPlus || b.MaxLevel() != 16 {
		t.Fatalf("proto %v max %d", b.Protocol(), b.MaxLevel())
	}
	waitWithin(t, 100*time.Millisecond, "init + 3 keepalives", func() bool { return len(f.Writes(testAddr)) >= 4 })
	w := f.Writes(testAddr)
	if !bytes.Equal(w[0], []byte{0xaa, 0x01, 0x00, 0x01, 0x55}) {
		t.Fatalf("first write % x", w[0])
	}
	for i, got := range w[1:4] {
		if want := FitPlusKeepalive(i); !bytes.Equal(got, want) {
			t.Fatalf("keepalive %d: % x want % x", i, got, want)
		}
	}
}

func TestFitPlusMaxLevelCapped(t *testing.T) {
	t.Parallel()
	_, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{MaxLevel: 99, Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if b.MaxLevel() != 32 {
		t.Fatalf("max %d", b.MaxLevel())
	}
}

func TestOpenUnsupportedAndMismatch(t *testing.T) {
	t.Parallel()
	_, c := connect(t, &bluetest.Device{Adv: blue.Advertisement{Addr: testAddr}})
	if _, err := Open(context.Background(), c, Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	_, c2 := connect(t, fitPlusDevice())
	if _, err := Open(context.Background(), c2, Options{Protocol: ProtoFTMS}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("forced FTMS on FitPlus: %v", err)
	}
}

func TestOpenFTMSRequestsControlAndReadsRange(t *testing.T) {
	t.Parallel()
	f, c := connect(t, ftmsDevice([]byte{0x0a, 0x00, 0x40, 0x01, 0x0a, 0x00}))
	b, err := Open(context.Background(), c, Options{ControlTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if b.Protocol() != ProtoFTMS || b.MaxLevel() != 32 {
		t.Fatalf("proto %v max %d", b.Protocol(), b.MaxLevel())
	}
	if w := f.Writes(testAddr); len(w) != 1 || !bytes.Equal(w[0], []byte{0x00}) {
		t.Fatalf("writes % x", w)
	}
}

func TestFTMSEncodingAndMax(t *testing.T) {
	t.Parallel()
	rng32 := []byte{0x0a, 0x00, 0x40, 0x01, 0x0a, 0x00}
	rng20 := []byte{0x0a, 0x00, 0xc8, 0x00, 0x0a, 0x00}
	cases := []struct {
		name    string
		rng     []byte
		o       Options
		wantMax int
		want    []byte // frame for SetLevel(20)
	}{
		{"range>25 forces u16 despite u8 option", rng32, Options{FTMSEnc: EncU8Tenths}, 32, []byte{0x04, 0xc8, 0x00}},
		{"range<=25 honours u8", rng20, Options{FTMSEnc: EncU8Tenths}, 20, []byte{0x04, 0xc8}},
		{"range<=25 honours u16", rng20, Options{FTMSEnc: EncU16Tenths}, 20, []byte{0x04, 0xc8, 0x00}},
		{"options max lowers range max", rng32, Options{MaxLevel: 24}, 24, []byte{0x04, 0xc8, 0x00}},
		{"options max cannot raise range max", rng20, Options{MaxLevel: 30}, 20, []byte{0x04, 0xc8}},
		{"no range defaults 16 so 20 clamps", nil, Options{}, 16, []byte{0x04, 0xa0}},
		{"no range, options max", nil, Options{MaxLevel: 20}, 20, []byte{0x04, 0xc8}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, c := connect(t, ftmsDevice(tc.rng))
			tc.o.ControlTimeout = time.Millisecond
			b, err := Open(context.Background(), c, tc.o)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close(context.Background())
			if b.MaxLevel() != tc.wantMax {
				t.Fatalf("max %d want %d", b.MaxLevel(), tc.wantMax)
			}
			if _, err := b.SetLevel(context.Background(), 20); err != nil {
				t.Fatal(err)
			}
			w := f.Writes(testAddr)
			if got := w[len(w)-1]; !bytes.Equal(got, tc.want) {
				t.Fatalf("frame % x want % x", got, tc.want)
			}
		})
	}
}

func TestSetLevelClampsAndDedups(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	ctx := context.Background()
	if sent, err := b.SetLevel(ctx, 0); err != nil || sent != 1 {
		t.Fatalf("sent %d err %v", sent, err)
	}
	if sent, err := b.SetLevel(ctx, 1); err != nil || sent != 1 {
		t.Fatalf("sent %d err %v", sent, err)
	}
	w := f.Writes(testAddr)
	if len(w) != 2 || !bytes.Equal(w[1], lvl(t, 1)) { // init + one level write
		t.Fatalf("writes % x", w)
	}
	if sent, _ := b.SetLevel(ctx, 1000); sent != 16 {
		t.Fatalf("high clamp %d", sent)
	}
	if n := len(f.Writes(testAddr)); n != 3 {
		t.Fatalf("writes %d", n)
	}
}

func TestSetLevelMinLevelOption(t *testing.T) {
	t.Parallel()
	_, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{MinLevel: 3, MaxLevel: 10, Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if sent, _ := b.SetLevel(context.Background(), 1); sent != 3 {
		t.Fatalf("sent %d", sent)
	}
}

func TestTelemetryFromNotify(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	frame := make([]byte, 15)
	frame[5], frame[6], frame[9], frame[10] = 7, 80, 0xc8, 0x00
	f.Notify(testAddr, FitPlusNotify, []byte{1, 2, 3}) // not telemetry: ignored
	f.Notify(testAddr, FitPlusNotify, frame)
	select {
	case tel := <-b.Telemetry():
		if tel.Level != 7 || tel.Cadence != 80 || tel.PowerW != 20 || !tel.At.Equal(now) {
			t.Fatalf("%+v", tel)
		}
	case <-time.After(time.Second):
		t.Fatal("no telemetry")
	}
}

func TestTelemetryFTMS(t *testing.T) {
	t.Parallel()
	f, c := connect(t, ftmsDevice(nil))
	b, err := Open(context.Background(), c, Options{ControlTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	f.Notify(testAddr, FTMSIndoorBikeData, []byte{0x44, 0x00, 0xe8, 0x03, 0xa0, 0x00, 0xc8, 0x00})
	var tel Telemetry
	select {
	case tel = <-b.Telemetry():
	case <-time.After(time.Second):
		t.Fatal("no telemetry")
	}
	if tel.PowerW != 200 || tel.Cadence != 80 || tel.At.IsZero() {
		t.Fatalf("%+v", tel)
	}
}

func TestTelemetryDropsOldestWhenFull(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	for i := 1; i <= 20; i++ {
		fr := make([]byte, 15)
		fr[5] = byte(i)
		f.Notify(testAddr, FitPlusNotify, fr)
	}
	ch := b.Telemetry()
	if len(ch) != 16 {
		t.Fatalf("buffered %d", len(ch))
	}
	if first := <-ch; first.Level != 5 {
		t.Fatalf("oldest kept = level %d, want 5", first.Level)
	}
}

func TestCloseSendsMinLevelFirst(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{MinLevel: 2, Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetLevel(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.Writes(testAddr)
	if got := w[len(w)-1]; !bytes.Equal(got, lvl(t, 2)) {
		t.Fatalf("last write % x", got)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("conn not closed")
	}
	if err := b.Close(context.Background()); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, err := b.SetLevel(context.Background(), 5); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
	if n := len(f.Writes(testAddr)); n != len(w) {
		t.Fatal("write after close")
	}
}

func TestCloseSendsLevelOneEvenWhenMinZero(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.Writes(testAddr)
	if !bytes.Equal(w[len(w)-1], lvl(t, 1)) {
		t.Fatalf("% x", w[len(w)-1])
	}
}

func TestRemoteDropStopsKeepalive(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "keepalive", func() bool { return len(f.Writes(testAddr)) >= 3 })
	f.DropLink(testAddr)
	select { // telemetry closes only when the session goroutine has exited
	case _, ok := <-b.Telemetry():
		if ok {
			t.Fatal("unexpected telemetry")
		}
	case <-time.After(time.Second):
		t.Fatal("session goroutine did not stop on drop")
	}
	n := len(f.Writes(testAddr))
	time.Sleep(30 * time.Millisecond)
	if got := len(f.Writes(testAddr)); got != n {
		t.Fatalf("writes after drop: %d -> %d", n, got)
	}
	if _, err := b.SetLevel(context.Background(), 5); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("SetLevel after drop: %v", err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.Writes(testAddr)); got != n {
		t.Fatalf("Close wrote after drop: %d -> %d", n, got)
	}
}

func TestSetLevelRejectedByFTMS(t *testing.T) {
	t.Parallel()
	d := ftmsDevice(nil)
	var f *bluetest.Fake
	var mu sync.Mutex
	result := byte(0x01)
	d.OnWrite = func(ch blue.UUID, data []byte) {
		if ch != FTMSControlPoint || len(data) == 0 {
			return
		}
		mu.Lock()
		r := result
		mu.Unlock()
		if data[0] == 0x00 {
			r = 0x01
		}
		f.Notify(testAddr, FTMSControlPoint, []byte{0x80, data[0], r})
	}
	fk := bluetest.NewFake(d)
	f = fk
	c, err := fk.Connect(context.Background(), testAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = fk.Close() })
	b, err := Open(context.Background(), c, Options{ControlTimeout: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if _, err := b.SetLevel(context.Background(), 5); err != nil {
		t.Fatalf("success response: %v", err)
	}
	mu.Lock()
	result = 0x04 // operation failed
	mu.Unlock()
	if _, err := b.SetLevel(context.Background(), 6); !errors.Is(err, ErrControlRejected) {
		t.Fatalf("got %v", err)
	}
	// rejected level must not be remembered as sent: retry writes again
	n := len(fk.Writes(testAddr))
	if _, err := b.SetLevel(context.Background(), 6); !errors.Is(err, ErrControlRejected) {
		t.Fatalf("retry got %v", err)
	}
	if len(fk.Writes(testAddr)) != n+1 {
		t.Fatal("rejected level was deduped")
	}
}

func TestOpenContextCanceled(t *testing.T) {
	t.Parallel()
	_, c := connect(t, fitPlusDevice())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, c, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

// flakyConn wraps a Conn to simulate stuck or failing writes.
type flakyConn struct {
	blue.Conn
	mu        sync.Mutex
	blockNext int  // number of upcoming writes that block until ctx is done
	failNext  bool // next write is delivered, then reports an error
}

func (c *flakyConn) Write(ctx context.Context, ch blue.UUID, data []byte, resp bool) error {
	c.mu.Lock()
	block, fail := c.blockNext > 0, c.failNext
	if block {
		c.blockNext--
	}
	c.failNext = false
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	err := c.Conn.Write(ctx, ch, data, resp)
	if err == nil && fail {
		return errors.New("simulated ack loss")
	}
	return err
}

func (c *flakyConn) set(f func()) { c.mu.Lock(); f(); c.mu.Unlock() }

func openFlaky(t *testing.T, o Options) (*bluetest.Fake, *flakyConn, *Bike) {
	t.Helper()
	f, c := connect(t, fitPlusDevice())
	fc := &flakyConn{Conn: c}
	o.Keepalive = time.Hour
	b, err := Open(context.Background(), fc, o)
	if err != nil {
		t.Fatal(err)
	}
	return f, fc, b
}

func TestFailedWriteInvalidatesDedup(t *testing.T) {
	t.Parallel()
	f, fc, b := openFlaky(t, Options{})
	ctx := context.Background()
	if _, err := b.SetLevel(ctx, 5); err != nil {
		t.Fatal(err)
	}
	fc.set(func() { fc.failNext = true }) // frame for 9 is delivered, but the write errors
	if _, err := b.SetLevel(ctx, 9); err == nil {
		t.Fatal("expected error")
	}
	n := len(f.Writes(testAddr))
	if sent, err := b.SetLevel(ctx, 5); err != nil || sent != 5 {
		t.Fatalf("sent %d err %v", sent, err)
	}
	w := f.Writes(testAddr)
	if len(w) != n+1 || !bytes.Equal(w[n], lvl(t, 5)) {
		t.Fatalf("level 5 was wrongly deduped after failed write (writes %d -> %d)", n, len(w))
	}
	_ = b.Close(ctx)
}

func TestWriteTimeoutBoundsSetLevelAndClose(t *testing.T) {
	t.Parallel()
	_, fc, b := openFlaky(t, Options{WriteTimeout: 20 * time.Millisecond})
	fc.set(func() { fc.blockNext = 2 })
	start := time.Now()
	if _, err := b.SetLevel(context.Background(), 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SetLevel: %v", err)
	}
	if err := b.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCloseAbortsStuckSetLevel(t *testing.T) {
	t.Parallel()
	// WriteTimeout is huge: only Close's cancel can release the stuck write.
	f, fc, b := openFlaky(t, Options{WriteTimeout: time.Minute})
	fc.set(func() { fc.blockNext = 1 })
	errc := make(chan error, 1)
	go func() { _, err := b.SetLevel(context.Background(), 5); errc <- err }()
	waitFor(t, "SetLevel to block", func() bool {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fc.blockNext == 0
	})
	done := make(chan error, 1)
	go func() { done <- b.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close queued behind a stuck write")
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("stuck SetLevel should fail")
		}
	case <-time.After(time.Second):
		t.Fatal("SetLevel not released")
	}
	w := f.Writes(testAddr)
	if !bytes.Equal(w[len(w)-1], lvl(t, 1)) {
		t.Fatalf("Close did not send min level last: % x", w[len(w)-1])
	}
}

func TestResendBypassesDedup(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := b.SetLevel(ctx, 5); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := b.Resend(ctx, 5); err != nil || got != 5 {
		t.Fatalf("resend %d %v", got, err)
	}
	n := 0
	for _, w := range f.Writes(testAddr) {
		if bytes.Equal(w, lvl(t, 5)) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("level-5 frames %d, want 2", n)
	}
	if got, err := b.Resend(ctx, 0); err != nil || got != 1 {
		t.Fatalf("resend(0) = %d %v, want 1", got, err)
	}
	w := f.Writes(testAddr)
	if !bytes.Equal(w[len(w)-1], lvl(t, 1)) {
		t.Fatalf("last write % x", w[len(w)-1])
	}
}

func TestBikeIsSession(t *testing.T) {
	t.Parallel()
	var _ Session = (*Bike)(nil)
}

func TestProtoSimString(t *testing.T) {
	t.Parallel()
	if got := ProtoSim.String(); got != "sim" {
		t.Fatalf("got %q", got)
	}
}

func TestProtoHAString(t *testing.T) {
	t.Parallel()
	if got := ProtoHA.String(); got != "ha" || ProtoHA != 4 {
		t.Fatalf("got %q (%d)", got, ProtoHA)
	}
}
