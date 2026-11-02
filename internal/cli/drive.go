package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
)

const closeTimeout = 3 * time.Second

// sweepLevels is min..max..min with the peak visited once (1,2,3,2,1).
func sweepLevels(lo, hi int) []int {
	var out []int
	for l := lo; l <= hi; l++ {
		out = append(out, l)
	}
	for l := hi - 1; l >= lo; l-- {
		out = append(out, l)
	}
	return out
}

// summarize derives a level row from samples taken in [start,end]: the
// reported level is the mode over the settled last half (from half), falling
// back to the whole window if that half is empty; averages use the same set.
func summarize(sent int, samples []bike.Telemetry, start, half, end time.Time) levelRow {
	var win, late []bike.Telemetry
	for _, s := range samples {
		if s.At.Before(start) || s.At.After(end) {
			continue
		}
		win = append(win, s)
		if !s.At.Before(half) {
			late = append(late, s)
		}
	}
	use := late
	if len(use) == 0 {
		use = win
	}
	row := levelRow{Sent: sent, Samples: len(use)}
	if len(use) == 0 {
		return row
	}
	counts := map[int]int{}
	best, bestN := 0, 0
	var cad, pow float64
	for _, s := range use {
		cad += s.Cadence
		pow += s.PowerW
		counts[s.Level]++
		if counts[s.Level] >= bestN { // ties favour the most recent level
			best, bestN = s.Level, counts[s.Level]
		}
	}
	row.Reported = best
	row.AvgCadence = cad / float64(len(use))
	row.AvgPower = pow / float64(len(use))
	return row
}

type sweepOutcome int

const (
	sweepDone sweepOutcome = iota
	sweepInterrupted
	sweepDropped
	sweepFailed
)

// cmdDrive sweeps resistance levels, records what the bike reports and always
// returns the bike to its minimum level (never 0) on the way out.
func cmdDrive(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	fs, tf := newFlagSet("drive", stderr)
	addrS := fs.String("addr", "", "device address AA:BB:CC:DD:EE:FF[/random] (required)")
	protoS := fs.String("proto", "auto", "protocol: auto|fitplus|ftms")
	encS := fs.String("ftms-enc", "u8", "FTMS resistance encoding: u8|u16")
	minL := fs.Int("min", 1, "lowest level (never below 1)")
	maxL := fs.Int("max", 16, "highest level (capped by the device)")
	dwell := fs.Duration("dwell", 5*time.Second, "time to hold each level")
	reportPath := fs.String("report", "", "write a markdown report to this file")
	if code, ok := parseFlags(fs, tf, args, stderr); !ok {
		return code
	}
	addr, err := blue.ParseAddress(*addrS)
	if err != nil {
		fmt.Fprintf(stderr, "wattson drive: --addr: %v\n", err)
		return 2
	}
	opts := bike.Options{MaxLevel: *maxL, MinLevel: *minL}
	switch *protoS {
	case "auto":
	case "fitplus":
		opts.Protocol = bike.ProtoFitPlus
	case "ftms":
		opts.Protocol = bike.ProtoFTMS
	default:
		fmt.Fprintf(stderr, "wattson drive: bad --proto %q (auto|fitplus|ftms)\n", *protoS)
		return 2
	}
	switch *encS {
	case "u8":
		opts.FTMSEnc = bike.EncU8Tenths
	case "u16":
		opts.FTMSEnc = bike.EncU16Tenths
	default:
		fmt.Fprintf(stderr, "wattson drive: bad --ftms-enc %q (u8|u16)\n", *encS)
		return 2
	}
	if *dwell <= 0 || *maxL < 1 {
		fmt.Fprintln(stderr, "wattson drive: --dwell and --max must be positive")
		return 2
	}
	if *minL < 1 {
		fmt.Fprintln(stderr, "wattson drive: level 0 is never sent; using --min 1")
	}
	if d.tweakBike != nil {
		d.tweakBike(&opts)
	}

	tr, ok := openTransportCtx(ctx, d, tf, stderr)
	if !ok {
		return 1
	}
	defer tr.Close()
	fmt.Fprintf(stderr, "connecting to %s...\n", addr)
	conn, err := connectTo(ctx, tr, addr)
	if err != nil {
		fmt.Fprintf(stderr, "wattson drive: connect: %v\n", err)
		return 1
	}
	octx, ocancel := context.WithTimeout(ctx, 15*time.Second)
	b, err := bike.Open(octx, conn, opts)
	ocancel()
	if err != nil {
		_ = conn.Close()
		fmt.Fprintf(stderr, "wattson drive: open bike: %v\n", err)
		return 1
	}
	// Whatever happens below, drop to the minimum level and release the link.
	var closeOnce sync.Once
	closeBike := func() {
		closeOnce.Do(func() {
			cctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			defer cancel()
			if err := b.Close(cctx); err != nil {
				fmt.Fprintf(stderr, "wattson drive: warning: closing bike: %v\n", err)
			}
		})
	}
	defer closeBike()

	name := deviceName(ctx, conn) // read before the sweep: the link is closed afterwards
	lo := min(max(1, *minL), b.MaxLevel())
	hi := b.MaxLevel()
	levels := sweepLevels(lo, hi)
	fmt.Fprintf(stdout, "protocol %s, levels %d..%d, dwell %s, %d steps. Ctrl-C returns the bike to level %d.\n",
		b.Protocol(), lo, hi, *dwell, len(levels), lo)

	var mu sync.Mutex
	var samples []bike.Telemetry
	go func() {
		for t := range b.Telemetry() {
			mu.Lock()
			samples = append(samples, t)
			mu.Unlock()
		}
	}()

	var rows []levelRow
	cur := 0
	outcome := sweepDone
sweep:
	for _, want := range levels {
		start := time.Now()
		sent, err := b.SetLevel(ctx, want)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			outcome = sweepInterrupted
			break sweep
		case isDone(conn):
			outcome = sweepDropped
			break sweep
		default:
			fmt.Fprintf(stderr, "wattson drive: set level %d: %v\n", want, err)
			outcome = sweepFailed
			break sweep
		}
		cur = sent
		set := time.Now()
		timer := time.NewTimer(*dwell)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			outcome = sweepInterrupted
			break sweep
		case <-conn.Done():
			timer.Stop()
			outcome = sweepDropped
			break sweep
		}
		mu.Lock()
		row := summarize(sent, samples, start, set.Add(*dwell/2), set.Add(*dwell))
		mu.Unlock()
		rows = append(rows, row)
		fmt.Fprintf(stdout, "level %2d: reported %-2s confirmed %-3s cadence %6s rpm power %6s W\n",
			row.Sent, row.reportedStr(), yesNo(row.confirmed()), row.cadenceStr(), row.powerStr())
	}

	closeBike()

	status := "complete"
	code := 0
	curStr := "(none set)"
	if cur > 0 {
		curStr = fmt.Sprint(cur)
	}
	switch outcome {
	case sweepInterrupted:
		status = fmt.Sprintf("INCOMPLETE (interrupted at level %s)", curStr)
		fmt.Fprintf(stderr, "interrupted at level %s; bike returned to level %d\n", curStr, lo)
		code = 130
	case sweepDropped:
		status = fmt.Sprintf("INCOMPLETE (bike disconnected at level %s)", curStr)
		fmt.Fprintf(stderr, "bike disconnected at level %s\n", curStr)
		code = 1
	case sweepFailed:
		status = fmt.Sprintf("INCOMPLETE (command failed at level %s)", curStr)
		fmt.Fprintf(stderr, "bike returned to level %d\n", lo)
		code = 1
	}

	confirmed := 0
	for _, r := range rows {
		if r.confirmed() {
			confirmed++
		}
	}
	fmt.Fprintf(stdout, "confirmed %d/%d levels\n", confirmed, len(rows))

	if *reportPath != "" {
		rep := report{
			At: d.now(), Name: name, Addr: addr, Transport: tf.label(),
			Protocol: b.Protocol().String(), MaxLevel: hi, Services: conn.Services(), Rows: rows, Status: status,
		}
		var sb strings.Builder
		rep.writeMarkdown(&sb)
		werr := os.MkdirAll(filepath.Dir(*reportPath), 0o755)
		if werr == nil {
			werr = os.WriteFile(*reportPath, []byte(sb.String()), 0o644)
		}
		if err := werr; err != nil {
			fmt.Fprintf(stderr, "wattson drive: write report: %v\n", err)
			if code == 0 {
				code = 1
			}
		} else {
			fmt.Fprintf(stdout, "report written to %s\n", *reportPath)
		}
	}
	return code
}

func isDone(c blue.Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}
