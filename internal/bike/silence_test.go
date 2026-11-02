package bike

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

func telFrame() []byte {
	f := make([]byte, 15)
	f[5], f[6], f[9], f[10] = 7, 80, 0xc8, 0x00
	return f
}

func isDone(b *Bike) bool {
	select {
	case <-b.Done():
		return true
	default:
		return false
	}
}

func waitDone(t *testing.T, b *Bike) {
	t.Helper()
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
}

func TestSilenceWatchdogTreatsLinkAsDown(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: 5 * time.Millisecond, SilenceTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	// Not armed before the first frame.
	time.Sleep(40 * time.Millisecond)
	if isDone(b) {
		t.Fatal("watchdog fired before any telemetry")
	}
	f.Notify(testAddr, FitPlusNotify, telFrame())
	waitDone(t, b)
	if err := b.Err(); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("Err = %v, want ErrDisconnected", err)
	}
	if !errors.Is(b.Err(), ErrSilence) {
		t.Fatalf("Err = %v, want ErrSilence", b.Err())
	}
	select {
	case _, ok := <-b.Telemetry():
		for ok { // drain the buffered sample
			_, ok = <-b.Telemetry()
		}
	case <-time.After(time.Second):
		t.Fatal("Telemetry not closed on silence")
	}
	if _, err := b.SetLevel(context.Background(), 5); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("SetLevel after silence: %v", err)
	}
	// Keepalive stopped.
	n := len(f.Writes(testAddr))
	time.Sleep(30 * time.Millisecond)
	if got := len(f.Writes(testAddr)); got != n {
		t.Fatalf("keepalive after silence: %d -> %d", n, got)
	}
	// The link may be alive but silent: Close still sends the minimum level.
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.Writes(testAddr)
	if len(w) != n+1 || !bytes.Equal(w[n], lvl(t, 1)) {
		t.Fatalf("Close did not send min level after silence (writes %d -> %d)", n, len(w))
	}
}

// fakeTimer is a manually driven silence timer.
type fakeTimer struct {
	ch      chan time.Time
	created atomic.Int32
	resets  atomic.Int32
	d       atomic.Int64
}

func (f *fakeTimer) mk(d time.Duration) silenceTimer {
	f.created.Add(1)
	f.d.Store(int64(d))
	return f
}
func (f *fakeTimer) C() <-chan time.Time { return f.ch }
func (f *fakeTimer) Reset(d time.Duration) {
	f.resets.Add(1)
	f.d.Store(int64(d))
}
func (f *fakeTimer) Stop() {}

func TestSilenceFramesRearmWatchdog(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	ft := &fakeTimer{ch: make(chan time.Time)}
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour, SilenceTimeout: time.Minute, silenceTimer: ft.mk})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if ft.created.Load() != 0 || isDone(b) {
		t.Fatal("armed before the first frame")
	}
	f.Notify(testAddr, FitPlusNotify, telFrame())
	waitFor(t, "arming", func() bool { return ft.created.Load() == 1 })
	if ft.d.Load() != int64(time.Minute) {
		t.Fatalf("timer duration %v", time.Duration(ft.d.Load()))
	}
	for i := 0; i < 3; i++ { // every frame re-arms; none ends the session
		f.Notify(testAddr, FitPlusNotify, telFrame())
		want := int32(i + 1)
		waitFor(t, "re-arm", func() bool { return ft.resets.Load() >= want })
	}
	if isDone(b) {
		t.Fatal("watchdog fired with telemetry flowing")
	}
	ft.ch <- time.Now() // the timer expires: no frame for SilenceTimeout
	waitDone(t, b)
	if !errors.Is(b.Err(), ErrSilence) {
		t.Fatalf("Err = %v", b.Err())
	}
}

func TestSilenceDisabledByDefault(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	f.Notify(testAddr, FitPlusNotify, telFrame())
	time.Sleep(40 * time.Millisecond)
	if isDone(b) || b.Err() != nil {
		t.Fatal("session ended without SilenceTimeout")
	}
}

func TestSilenceCloseReportsFailedWrite(t *testing.T) {
	t.Parallel()
	f, fc, b := openFlakySilence(t)
	f.Notify(testAddr, FitPlusNotify, telFrame())
	waitDone(t, b)
	fc.set(func() { fc.blockNext = 1 })
	if err := b.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want the bounded write's timeout", err)
	}
}

func openFlakySilence(t *testing.T) (*bluetest.Fake, *flakyConn, *Bike) {
	t.Helper()
	f, fc, b := openFlaky(t, Options{WriteTimeout: 20 * time.Millisecond, SilenceTimeout: 15 * time.Millisecond})
	return f, fc, b
}

func TestDoneOnDropAndOnClose(t *testing.T) {
	t.Parallel()
	f, c := connect(t, fitPlusDevice())
	b, err := Open(context.Background(), c, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if isDone(b) || b.Err() != nil {
		t.Fatal("done too early")
	}
	f.DropLink(testAddr)
	waitDone(t, b)
	if err := b.Err(); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("Err after drop = %v", err)
	}
	_ = b.Close(context.Background())

	_, c2 := connect(t, fitPlusDevice())
	b2, err := Open(context.Background(), c2, Options{Keepalive: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, b2)
	if b2.Err() != nil {
		t.Fatalf("Err after normal Close = %v", b2.Err())
	}
}
