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
	"syscall"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const usage = "usage: wattson <scan|probe|drive|version> [flags]  (run a subcommand with -h for its flags)\n"

// newTransport opens the BLE transport; tests replace it (or use deps).
var newTransport = openTransport

// deps are the seams tests use to run commands against a fake bike.
type deps struct {
	newTransport    func(ctx context.Context, via, key string) (blue.Transport, error)
	tweakBike       func(*bike.Options) // optional: adjust bike options (keepalive etc.)
	now             func() time.Time    // report timestamp
	selftestTimeout time.Duration       // probe --selftest notification wait
}

func defaultDeps() deps {
	return deps{newTransport: newTransport, now: time.Now, selftestTimeout: 3 * time.Second}
}

// Run dispatches args (without the program name) and returns the exit code.
// SIGINT/SIGTERM cancel the running command, which then drops the bike to its
// minimum level before returning.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, defaultDeps(), args, stdout, stderr)
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

// openTransportCtx opens the transport with a bounded setup time.
func openTransportCtx(ctx context.Context, d deps, tf *transportFlags, stderr io.Writer) (blue.Transport, bool) {
	octx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tr, err := d.newTransport(octx, tf.via, tf.key)
	if err != nil {
		fmt.Fprintf(stderr, "wattson: open transport %s: %v\n", tf.label(), err)
		return nil, false
	}
	return tr, true
}
