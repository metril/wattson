package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/metril/wattson/internal/blue"
)

func addrTypeName(t blue.AddrType) string {
	if t == blue.AddrRandom {
		return "random"
	}
	return "public"
}

// cmdScan lists advertising devices, de-duplicated and sorted by RSSI.
func cmdScan(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	fs, tf := newFlagSet("scan", stderr)
	dur := fs.Duration("duration", 10*time.Second, "how long to scan")
	prefix := fs.String("name-prefix", "", "only show devices whose name starts with this (case-insensitive)")
	if code, ok := parseFlags(fs, tf, args, stderr); !ok {
		return code
	}
	if *dur <= 0 {
		fmt.Fprintln(stderr, "wattson scan: --duration must be positive")
		return 2
	}
	tr, code := openTransportCtx(ctx, d, tf, stderr)
	if code != 0 {
		return code
	}
	defer tr.Close()

	fmt.Fprintf(stderr, "scanning %s for %s...\n", tf.label(), *dur)
	sctx, cancel := context.WithTimeout(ctx, *dur)
	defer cancel()
	seen := map[blue.Address]blue.Advertisement{}
	err := tr.Scan(sctx, func(a blue.Advertisement) {
		if p := strings.ToLower(*prefix); p != "" && !strings.HasPrefix(strings.ToLower(a.Name), p) {
			return
		}
		prev, had := seen[a.Addr]
		if had {
			if a.Name == "" {
				a.Name = prev.Name
			}
			if len(a.Services) == 0 {
				a.Services = prev.Services
			}
			if prev.RSSI > a.RSSI {
				a.RSSI = prev.RSSI // keep the strongest sighting
			}
		}
		seen[a.Addr] = a
	})
	if err != nil {
		fmt.Fprintf(stderr, "wattson scan: %v\n", err)
		return 1
	}

	advs := make([]blue.Advertisement, 0, len(seen))
	for _, a := range seen {
		advs = append(advs, a)
	}
	sort.Slice(advs, func(i, j int) bool {
		if advs[i].RSSI != advs[j].RSSI {
			return advs[i].RSSI > advs[j].RSSI
		}
		return advs[i].Addr.String() < advs[j].Addr.String()
	})
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ADDR\tTYPE\tRSSI\tNAME\tSERVICES")
	for _, a := range advs {
		svcs := make([]string, len(a.Services))
		for i, s := range a.Services {
			svcs[i] = s.String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", a.Addr.String(), addrTypeName(a.Addr.Type), a.RSSI, a.Name, strings.Join(svcs, ","))
	}
	tw.Flush()
	if len(advs) == 0 {
		fmt.Fprintln(stderr, "no devices found")
	}
	return 0
}

// macString is the MAC without the "/random" suffix (type is shown separately).
func macString(a blue.Address) string {
	a.Type = blue.AddrPublic
	return a.String()
}
