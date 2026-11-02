package bluetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/blue"
)

// Harness wires a Transport under test to a simulated peripheral.
type Harness struct {
	Transport  blue.Transport
	Dev        blue.Address
	NotifyChar blue.UUID    // must support notify or indicate
	WriteChar  blue.UUID    // must be writable
	ReadChar   blue.UUID    // must be readable
	ReadValue  []byte       // value the peripheral returns for ReadChar
	Push       func([]byte) // make the peripheral notify NotifyChar
	Drop       func()       // make the peripheral drop the link
	// Writes optionally returns the payloads the peripheral has received, in order.
	Writes func() [][]byte
}

// RunContract runs the transport conformance suite. newHarness is called once per
// subtest and must return an independent harness (xdist/parallel safe).
func RunContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	run := func(name string, fn func(t *testing.T, ctx context.Context, h Harness)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			fn(t, ctx, h)
		})
	}
	connect := func(t *testing.T, ctx context.Context, h Harness) blue.Conn {
		t.Helper()
		c, err := h.Transport.Connect(ctx, h.Dev)
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	run("ScanFindsDevice", func(t *testing.T, ctx context.Context, h Harness) {
		sctx, stop := context.WithCancel(ctx)
		defer stop()
		found := false
		err := h.Transport.Scan(sctx, func(a blue.Advertisement) {
			if a.Addr == h.Dev {
				found = true
				stop()
			}
		})
		if err != nil {
			t.Fatalf("Scan returned %v, want nil on ctx cancel", err)
		}
		if !found {
			t.Fatal("device not seen during scan")
		}
	})

	run("ConnectListsServices", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		has := map[blue.UUID]bool{}
		for _, s := range c.Services() {
			for _, ch := range s.Chars {
				has[ch.UUID] = true
			}
		}
		for _, u := range []blue.UUID{h.NotifyChar, h.WriteChar, h.ReadChar} {
			if !has[u] {
				t.Errorf("characteristic %s missing from Services()", u)
			}
		}
	})

	run("ReadReturnsValue", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		got, err := c.Read(ctx, h.ReadChar)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !bytes.Equal(got, h.ReadValue) {
			t.Fatalf("Read = %x, want %x", got, h.ReadValue)
		}
	})

	run("WriteReachesDevice", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		payload := []byte{0xaa, 0x01, 0x02}
		if err := c.Write(ctx, h.WriteChar, payload, true); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if h.Writes != nil {
			w := h.Writes()
			if len(w) != 1 || !bytes.Equal(w[0], payload) {
				t.Fatalf("device received %x, want [%x]", w, payload)
			}
		}
	})

	run("SubscribeReceivesNotify", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		got := make(chan []byte, 1)
		err := c.Subscribe(ctx, h.NotifyChar, func(b []byte) {
			select {
			case got <- append([]byte(nil), b...):
			default:
			}
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		want := []byte{1, 2, 3, 4}
		h.Push(want)
		select {
		case b := <-got:
			if !bytes.Equal(b, want) {
				t.Fatalf("notify = %x, want %x", b, want)
			}
		case <-ctx.Done():
			t.Fatal("no notification before deadline")
		}
	})

	run("RemoteDropClosesDone", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		h.Drop()
		select {
		case <-c.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("Done not closed after remote drop")
		}
		if err := c.Err(); !errors.Is(err, blue.ErrDisconnected) {
			t.Fatalf("Err = %v, want ErrDisconnected", err)
		}
	})

	run("LocalCloseNilErr", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		select {
		case <-c.Done():
		case <-ctx.Done():
			t.Fatal("Done not closed after Close")
		}
		if err := c.Err(); err != nil {
			t.Fatalf("Err = %v, want nil after local Close", err)
		}
	})

	run("ConnectUnknownTimesOut", func(t *testing.T, ctx context.Context, h Harness) {
		unknown := blue.Address{MAC: [6]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}}
		if unknown == h.Dev {
			unknown.MAC[5] = 2
		}
		const d = 50 * time.Millisecond
		cctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		start := time.Now()
		c, err := h.Transport.Connect(cctx, unknown)
		if err == nil {
			c.Close()
			t.Fatal("Connect to unknown device succeeded")
		}
		if el := time.Since(start); el > d+100*time.Millisecond {
			t.Fatalf("Connect returned after %v, want within ctx+100ms", el)
		}
	})

	run("WriteUnknownChar", func(t *testing.T, ctx context.Context, h Harness) {
		c := connect(t, ctx, h)
		err := c.Write(ctx, blue.UUID16(0xdead), []byte{1}, true)
		if !errors.Is(err, blue.ErrNoCharacteristic) {
			t.Fatalf("Write = %v, want ErrNoCharacteristic", err)
		}
	})
}
