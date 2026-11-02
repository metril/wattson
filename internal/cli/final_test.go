package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

func TestDriveSilenceTreatsBikeAsDisconnected(t *testing.T) {
	t.Parallel()
	f := newFitPlusFakeN(func(l int) int { return l }, 2) // handshake + first level, then silent
	d := testDeps(f)
	base := d.tweakBike
	d.tweakBike = func(o *bike.Options) {
		base(o)
		o.SilenceTimeout = 100 * time.Millisecond
	}
	start := time.Now()
	code, _, errs := runCLI(t, d, "drive", "--addr", testAddr.String(), "--dwell", "5s", "--max", "3")
	if code != 1 || !strings.Contains(errs, "no telemetry for 100ms — treating bike as disconnected at level 1") {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if w := levelWrites(f); len(w) == 0 || w[len(w)-1] != 1 {
		t.Fatalf("level writes %v: want min level last", w)
	}
	if strings.Contains(errs, "bike returned to level") {
		t.Fatalf("must not claim a confirmed return for a silent bike: %q", errs)
	}
}

func TestDriveDefaultSilenceTimeoutIs5s(t *testing.T) {
	t.Parallel()
	var got time.Duration
	d := testDeps(fitPlusEcho())
	d.tweakBike = func(o *bike.Options) { got = o.SilenceTimeout; o.Keepalive = 2 * time.Millisecond }
	runCLI(t, d, driveArgs("--max", "1")...)
	if got != 5*time.Second {
		t.Fatalf("SilenceTimeout = %v", got)
	}
}

func TestInterruptedWhileOpeningTransport(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	d := testDeps(nil)
	d.newTransport = func(ctx context.Context, _, _ string) (blue.Transport, error) {
		cancel()
		return nil, ctx.Err()
	}
	var out, errb bytes.Buffer
	code := run(ctx, d, []string{"drive", "--addr", testAddr.String()}, &out, &errb)
	if code != 130 || !strings.Contains(errb.String(), "interrupted before connecting") {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
}

func TestInterruptedDuringConnect(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"drive", "probe"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(20*time.Millisecond, cancel)
			var out, errb bytes.Buffer
			// Not on the fake: Connect blocks until its context ends.
			code := run(ctx, testDeps(fitPlusEcho()), []string{cmd, "--addr", "C0:FF:EE:00:00:99"}, &out, &errb)
			if code != 130 || !strings.Contains(errb.String(), "interrupted before connecting") || strings.Contains(errb.String(), "context canceled") {
				t.Fatalf("exit %d stderr %q", code, errb.String())
			}
		})
	}
}

// blockInitTransport makes the FitPlus init write block until its context ends.
type blockInitTransport struct{ blue.Transport }

func (t blockInitTransport) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	c, err := t.Transport.Connect(ctx, a)
	if err != nil {
		return nil, err
	}
	return blockInitConn{c}, nil
}

type blockInitConn struct{ blue.Conn }

func (c blockInitConn) Write(ctx context.Context, ch blue.UUID, data []byte, resp bool) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestInterruptedDuringBikeOpen(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	var out, errb bytes.Buffer
	code := run(ctx, testDeps(blockInitTransport{f}), driveArgs(), &out, &errb)
	if code != 130 || !strings.Contains(errb.String(), "interrupted before connecting") || strings.Contains(errb.String(), "context canceled") {
		t.Fatalf("exit %d stderr %q", code, errb.String())
	}
	if w := levelWrites(f); len(w) != 0 {
		t.Fatalf("levels written: %v", w)
	}
}

func TestWatchSignalsSecondForcesExit(t *testing.T) {
	t.Parallel()
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	var cancelled atomic.Bool
	exited := make(chan int, 1)
	var errb bytes.Buffer
	finished := make(chan struct{})
	go func() {
		watchSignals(sigs, done, func() { cancelled.Store(true) }, func() int { return 7 }, &errb, func(c int) { exited <- c })
		close(finished)
	}()
	sigs <- os.Interrupt
	for !cancelled.Load() {
		time.Sleep(time.Millisecond)
	}
	select {
	case c := <-exited:
		t.Fatalf("exited %d after first signal", c)
	case <-time.After(20 * time.Millisecond):
	}
	sigs <- os.Interrupt
	if c := <-exited; c != 130 {
		t.Fatalf("exit %d", c)
	}
	<-finished
	if !strings.Contains(errb.String(), "forced exit; bike may still be at level 7 — lower resistance at the console") {
		t.Fatalf("stderr %q", errb.String())
	}
	close(done)
}

func TestWatchSignalsNoSignalReturnsOnDone(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		watchSignals(make(chan os.Signal), done, func() {}, func() int { return 0 }, &bytes.Buffer{}, func(int) { t.Error("exit called") })
		close(finished)
	}()
	close(done)
	<-finished
}

func esphomeCLIDeps(t *testing.T, srv *esphometest.Server) deps {
	t.Helper()
	d := testDeps(nil)
	d.newTransport = openTransport
	d.connectTimeout = 80 * time.Millisecond
	return d
}

func TestESPHomeConnectTimeoutHintsRandom(t *testing.T) {
	t.Parallel()
	k := bytes.Repeat([]byte{3}, 32)
	srv := esphometest.Start(t, k)
	srv.HandleHello(0x3f)
	srv.AddBLEDevice(esphometest.RawAdv{Address: 0x0a0b0c0d0e0f, Data: []byte{2, 1, 6}}, nil)
	d := esphomeCLIDeps(t, srv)
	key := base64.StdEncoding.EncodeToString(k)
	for _, cmd := range []string{"drive", "probe"} {
		for _, tc := range []struct {
			addr string
			hint bool
		}{{"C0:FF:EE:00:00:01", true}, {"C0:FF:EE:00:00:01/random", false}} {
			code, _, errs := runCLI(t, d, cmd, "--via", "esphome://"+srv.Addr(), "--key", key, "--addr", tc.addr)
			hinted := strings.Contains(errs, "pass --addr C0:FF:EE:00:00:01/random")
			if code != 1 || hinted != tc.hint {
				t.Fatalf("%s %s: exit %d hint=%v stderr %q", cmd, tc.addr, code, hinted, errs)
			}
		}
	}
}

func TestESPHomeRefusedConnectionIsClear(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, bytes.Repeat([]byte{3}, 32))
	addr := srv.Addr()
	srv.Close()
	d := testDeps(nil)
	d.newTransport = openTransport
	code, _, errs := runCLI(t, d, "scan", "--via", "esphome://"+addr, "--key", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if code != 1 || !strings.Contains(errs, "connection refused") || !strings.Contains(errs, addr) {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
}
