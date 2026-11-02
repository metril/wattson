// Command wattson probes and drives a Merach exercise bike over BLE.
package main

import (
	"os"

	"github.com/metril/wattson/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
