package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
)

var gapDeviceName = blue.UUID16(0x2a00)

// connectTo connects with a bounded wait.
func connectTo(ctx context.Context, tr blue.Transport, addr blue.Address) (blue.Conn, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return tr.Connect(cctx, addr)
}

// deviceName reads the GAP device name, or "" if unavailable.
func deviceName(ctx context.Context, c blue.Conn) string {
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := c.Read(rctx, gapDeviceName)
	if err != nil {
		return ""
	}
	return string(b)
}

// sink serialises probe output to stdout and the optional hexlog file.
type sink struct {
	mu     sync.Mutex
	stdout io.Writer
	file   io.Writer
	closed bool
}

func (s *sink) printf(format string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		fmt.Fprintf(s.stdout, format, a...)
	}
}

func (s *sink) hexLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	fmt.Fprintln(s.stdout, line)
	if s.file != nil {
		fmt.Fprintln(s.file, line)
	}
}

func (s *sink) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// cmdProbe dumps the GATT tree, detects the protocol and logs notifications.
func cmdProbe(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	fs, tf := newFlagSet("probe", stderr)
	addrS := fs.String("addr", "", "device address AA:BB:CC:DD:EE:FF[/random] (required)")
	dur := fs.Duration("duration", 30*time.Second, "how long to log notifications")
	out := fs.String("out", "", "also write `t_ms char hex` lines to this file (truncated)")
	selftest := fs.Bool("selftest", false, "write the protocol handshake and expect a notification within 3s")
	if code, ok := parseFlags(fs, tf, args, stderr); !ok {
		return code
	}
	addr, err := blue.ParseAddress(*addrS)
	if err != nil {
		fmt.Fprintf(stderr, "wattson probe: --addr: %v\n", err)
		return 2
	}
	if *dur <= 0 {
		fmt.Fprintln(stderr, "wattson probe: --duration must be positive")
		return 2
	}
	sk := &sink{stdout: stdout}

	tr, ok := openTransportCtx(ctx, d, tf, stderr)
	if !ok {
		return 1
	}
	defer tr.Close()
	fmt.Fprintf(stderr, "connecting to %s...\n", addr)
	conn, err := connectTo(ctx, tr, addr)
	if err != nil {
		fmt.Fprintf(stderr, "wattson probe: connect: %v\n", err)
		return 1
	}
	defer conn.Close()
	defer sk.close()
	if *out != "" { // only now: a failed connect must not truncate an earlier log
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "wattson probe: %v\n", err)
			return 1
		}
		defer f.Close()
		sk.file = f
	}

	svcs := conn.Services()
	proto := bike.Detect(svcs)
	if n := deviceName(ctx, conn); n != "" {
		sk.printf("device: %s\n", n)
	}
	sk.printf("address: %s (%s)\ntransport: %s\nprotocol: %s\nservices:\n", macString(addr), addrTypeName(addr.Type), tf.label(), proto)
	var tree strings.Builder
	writeTree(&tree, svcs)
	sk.printf("%s", tree.String())

	t0 := time.Now()
	// Selftest passes only on the characteristic that answers the handshake:
	// FitPlus notify (fff1), or the FTMS control point (2ad9). A bike streaming
	// 2ad2 must not satisfy it.
	ansCh := selftestAnswerChar(proto)
	got := make(chan []byte, 16)
	subs := 0
	for _, s := range svcs {
		for _, c := range s.Chars {
			if c.Props&(blue.PropNotify|blue.PropIndicate) == 0 {
				continue
			}
			name := c.UUID.String()
			if err := conn.Subscribe(ctx, c.UUID, func(b []byte) {
				sk.hexLine(fmt.Sprintf("%d %s %s", time.Since(t0).Milliseconds(), name, hex.EncodeToString(b)))
				if *selftest && c.UUID == ansCh {
					select {
					case got <- append([]byte(nil), b...):
					default:
					}
				}
			}); err != nil {
				fmt.Fprintf(stderr, "wattson probe: subscribe %s: %v\n", name, err)
				continue
			}
			subs++
		}
	}
	sk.printf("subscribed to %d characteristic(s); logging %s (Ctrl-C to stop)\n", subs, *dur)

	if *selftest {
		if code := probeSelftest(ctx, d, conn, proto, got, sk); code != 0 {
			return code
		}
	}

	timer := time.NewTimer(*dur)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	case <-conn.Done():
		fmt.Fprintln(stderr, "wattson probe: device disconnected")
		return 1
	}
	return 0
}

// probeSelftest sends the harmless protocol opener and waits for the answer
// on the protocol's answer characteristic (see selftestAnswerChar).
func probeSelftest(ctx context.Context, d deps, conn blue.Conn, proto bike.Protocol, got chan []byte, sk *sink) int {
	ansCh := selftestAnswerChar(proto)
	var ch blue.UUID
	var frame []byte
	switch proto {
	case bike.ProtoFitPlus:
		ch, frame = bike.FitPlusWrite, bike.FitPlusInit()
	case bike.ProtoFTMS:
		ch, frame = bike.FTMSControlPoint, bike.FTMSRequestControl()
	default:
		sk.printf("selftest: FAIL (no supported protocol detected)\n")
		return 1
	}
	for drained := false; !drained; { // drop answers that predate the write
		select {
		case <-got:
		default:
			drained = true
		}
	}
	start := time.Now()
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := conn.Write(wctx, ch, frame, needsResponse(conn, ch))
	cancel()
	if err != nil {
		sk.printf("selftest: FAIL (write %s: %v)\n", ch, err)
		return 1
	}
	timer := time.NewTimer(d.selftestTimeout)
	defer timer.Stop()
	for {
		select {
		case p := <-got:
			if proto == bike.ProtoFTMS {
				if r, err := bike.ParseControlResponse(p); err != nil || r.Op != 0x00 || r.Result != 0x01 {
					continue // not the Request Control success response
				}
			}
			sk.printf("selftest: PASS (%s write answered on %s in %d ms)\n", proto, ansCh, time.Since(start).Milliseconds())
			return 0
		case <-timer.C:
			sk.printf("selftest: FAIL (no answer on %s within %s)\n", ansCh, d.selftestTimeout)
		case <-ctx.Done():
			sk.printf("selftest: interrupted\n")
		case <-conn.Done():
			sk.printf("selftest: FAIL (disconnected)\n")
		}
		return 1
	}
}

// selftestAnswerChar is the characteristic that must answer the handshake.
func selftestAnswerChar(p bike.Protocol) blue.UUID {
	if p == bike.ProtoFTMS {
		return bike.FTMSControlPoint
	}
	return bike.FitPlusNotify
}

// needsResponse mirrors the bike session: write with response unless the
// characteristic only supports write-without-response.
func needsResponse(c blue.Conn, ch blue.UUID) bool {
	for _, s := range c.Services() {
		for _, x := range s.Chars {
			if x.UUID == ch {
				return x.Props&blue.PropWriteNoResp == 0
			}
		}
	}
	return true
}
