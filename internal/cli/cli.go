// Package cli implements the wattson command-line dispatcher.
package cli

import (
	"fmt"
	"io"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const usage = "usage: wattson <scan|probe|drive|version>\n"

// Run dispatches args (without the program name) and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "wattson %s\n", Version)
		return 0
	case "scan", "probe", "drive":
		fmt.Fprintf(stderr, "wattson %s: not implemented\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "wattson: unknown subcommand %q\n%s", args[0], usage)
		return 2
	}
}
