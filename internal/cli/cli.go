// Package cli implements the wattson command-line dispatcher.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const usage = "usage: wattson <scan|probe|drive|ride|version> [flags]  (run a subcommand with -h for its flags)\n"

// newTransport opens the BLE transport; tests replace it (or use deps).
var newTransport = openTransport

// deps are the seams tests use to run commands against a fake bike.
type deps struct {
	newTransport    func(ctx context.Context, via, key string) (blue.Transport, error)
	tweakBike       func(*bike.Options)        // optional: adjust bike options (keepalive etc.)
	now             func() time.Time           // report timestamp
	selftestTimeout time.Duration              // probe --selftest notification wait
	connectTimeout  time.Duration              // bound on one BLE connect; 0 = 30s
	simDial         func(sim.Config) bike.Dial // ride --sim; nil = sim.Dial
}

// exitInterrupted is the conventional exit code after SIGINT.
const exitInterrupted = 130

// lastLevel is the level drive most recently set (0 = none yet); a forced exit
// reports it so the rider can lower resistance by hand.
var lastLevel atomic.Int32

func defaultDeps() deps {
	return deps{newTransport: newTransport, now: time.Now, selftestTimeout: 3 * time.Second, simDial: sim.Dial}
}

// Run dispatches args (without the program name) and returns the exit code.
// The first SIGINT/SIGTERM cancels the running command, which then drops the
// bike to its minimum level before returning; a second one exits immediately.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	done := make(chan struct{})
	defer close(done)
	go watchSignals(sigs, done, cancel, func() int { return int(lastLevel.Load()) }, stderr, os.Exit)
	return run(ctx, defaultDeps(), args, stdout, stderr)
}

// watchSignals cancels on the first signal and force-exits on the second.
func watchSignals(sigs <-chan os.Signal, done <-chan struct{}, cancel func(), level func() int, stderr io.Writer, exit func(int)) {
	select {
	case <-sigs:
	case <-done:
		return
	}
	cancel()
	select {
	case <-sigs:
	case <-done:
		return
	}
	if n := level(); n > 0 {
		fmt.Fprintf(stderr, "forced exit; bike may still be at level %d — lower resistance at the console\n", n)
	} else {
		fmt.Fprintln(stderr, "forced exit; no resistance level had been set")
	}
	exit(exitInterrupted)
}

func run(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "wattson %s\n", Version)
		return 0
	case "scan":
		return cmdScan(ctx, d, args[1:], stdout, stderr)
	case "probe":
		return cmdProbe(ctx, d, args[1:], stdout, stderr)
	case "drive":
		return cmdDrive(ctx, d, args[1:], stdout, stderr)
	case "ride":
		return cmdRide(ctx, d, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "wattson: unknown subcommand %q\n%s", args[0], usage)
		return 2
	}
}

// newFlagSet returns a flag set with the shared --via/--key flags registered.
func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *transportFlags) {
	fs := flag.NewFlagSet("wattson "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	tf := &transportFlags{}
	tf.register(fs)
	return fs, tf
}

// parseFlags parses args; ok=false means the caller should return code.
func parseFlags(fs *flag.FlagSet, tf *transportFlags, args []string, stderr io.Writer) (code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "wattson %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	if err := tf.validate(); err != nil {
		fmt.Fprintf(stderr, "wattson %s: %v\n", fs.Name(), err)
		return 2, false
	}
	return 0, true
}

// openTransportCtx opens the transport with a bounded setup time. code is 0
// on success, otherwise the exit code to return (130 when interrupted).
func openTransportCtx(ctx context.Context, d deps, tf *transportFlags, stderr io.Writer) (tr blue.Transport, code int) {
	octx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tr, err := d.newTransport(octx, tf.via, tf.key)
	if err != nil {
		if interrupted(ctx) {
			fmt.Fprintln(stderr, "wattson: interrupted before connecting")
			return nil, exitInterrupted
		}
		fmt.Fprintf(stderr, "wattson: open transport %s: %v\n", tf.label(), err)
		return nil, 1
	}
	return tr, 0
}

// connectHint explains a likely cause of a failed ESPHome connect: the device
// was addressed as public but is advertising a random address.
func connectHint(err error, tf *transportFlags, addr blue.Address) string {
	if strings.HasPrefix(tf.via, esphomePrefix) && addr.Type == blue.AddrPublic && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("\nhint: if the scan showed this device as random, pass --addr %s/random", macString(addr))
	}
	return ""
}

// interrupted reports whether ctx was cancelled (Ctrl-C), as opposed to
// expiring on a deadline.
func interrupted(ctx context.Context) bool { return errors.Is(ctx.Err(), context.Canceled) }
