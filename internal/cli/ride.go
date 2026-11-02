package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/engine"
)

// formatTick renders one ride line, e.g.
// "00:01:05 riding   L 8 ( 8)  80rpm  190W  31.2km/h   0.512km".
func formatTick(t engine.Tick) string {
	rep := "-"
	if t.ReportedLevel > 0 {
		rep = strconv.Itoa(t.ReportedLevel)
	}
	return fmt.Sprintf("%s %-8s L%2d (%2s) %3.0frpm %4.0fW %5.1fkm/h %7.3fkm",
		clock(t.ElapsedS), t.State, t.Level, rep, t.CadenceRpm, t.PowerW, t.SpeedKmh, t.DistanceM/1000)
}

// clock formats seconds as hh:mm:ss.
func clock(s float64) string {
	n := int(math.Round(s))
	return fmt.Sprintf("%02d:%02d:%02d", n/3600, n/60%60, n%60)
}

// cmdRide runs a headless Free-mode ride at 1 Hz over the simulator or a real
// bike and always returns the bike to its minimum level on the way out.
func cmdRide(ctx context.Context, d deps, args []string, stdout, stderr io.Writer) int {
	fs, tf := newFlagSet("ride", stderr)
	simF := fs.Bool("sim", false, "ride the built-in simulated bike")
	addrS := fs.String("addr", "", "device address AA:BB:CC:DD:EE:FF[/random]")
	protoS := fs.String("proto", "auto", "protocol: auto|fitplus|ftms")
	duration := fs.Duration("duration", 0, "riding time to stop after (0 = until Ctrl-C)")
	level := fs.Int("level", 5, "start resistance level")
	cadence := fs.Float64("cadence", 80, "simulated cadence in rpm (--sim only)")
	minL := fs.Int("min", 1, "lowest level (never below 1)")
	maxL := fs.Int("max", 16, "highest level (capped by the device)")
	mass := fs.Float64("mass", 75, "rider + bike mass in kg")
	if code, ok := parseFlags(fs, tf, args, stderr); !ok {
		return code
	}
	if *simF == (*addrS != "") {
		fmt.Fprintln(stderr, "wattson ride: need exactly one of --sim or --addr")
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
		fmt.Fprintf(stderr, "wattson ride: bad --proto %q (auto|fitplus|ftms)\n", *protoS)
		return 2
	}
	if *maxL < 1 || *duration < 0 || *cadence < 0 || *mass <= 0 {
		fmt.Fprintln(stderr, "wattson ride: --max and --mass must be positive; --duration and --cadence must not be negative")
		return 2
	}
	if *minL < 1 {
		fmt.Fprintln(stderr, "wattson ride: level 0 is never sent; using --min 1")
	}
	lo := min(max(1, *minL), *maxL)

	var dial bike.Dial
	if *simF {
		mk := d.simDial
		if mk == nil {
			mk = sim.Dial
		}
		dial = mk(sim.Config{MaxLevel: *maxL, Cadence: sim.Const(*cadence)})
	} else {
		addr, err := blue.ParseAddress(*addrS)
		if err != nil {
			fmt.Fprintf(stderr, "wattson ride: --addr: %v\n", err)
			return 2
		}
		if d.tweakBike != nil {
			d.tweakBike(&opts)
		}
		tr, code := openTransportCtx(ctx, d, tf, stderr)
		if code != 0 {
			return code
		}
		defer tr.Close()
		dial = bike.BLEDial(tr, addr, opts)
	}

	phys := engine.DefaultPhysics()
	phys.MassKg = *mass
	sup := bike.NewSupervisor(dial, bike.SupervisorOptions{MinLevel: lo, MaxLevel: *maxL})
	eng := engine.New(engine.Config{Driver: sup, StartLevel: *level, Physics: phys})
	ticks, unsub := eng.Subscribe(64)
	defer unsub()

	runErr := make(chan error, 1)
	go func() { runErr <- eng.Run(ctx) }()
	fmt.Fprintf(stderr, "riding in free mode from level %d; Ctrl-C returns the bike to level %d.\n", *level, lo)

	started, stopping, connected := false, false, false
	for tk := range ticks {
		if tk.Connected != connected {
			connected = tk.Connected
			if connected {
				fmt.Fprintln(stderr, "bike connected")
			} else {
				fmt.Fprintln(stderr, "bike disconnected; reconnecting")
			}
		}
		if tk.Alert != "" {
			fmt.Fprintf(stderr, "alert: %s\n", tk.Alert)
		}
		if tk.State == engine.StateReady && !started {
			if err := eng.Do(ctx, engine.Command{Kind: engine.CmdStart}); err == nil {
				started = true
			}
		}
		if tk.State != engine.StateRiding && tk.State != engine.StatePaused {
			continue
		}
		lastLevel.Store(int32(tk.Level))
		fmt.Fprintln(stdout, formatTick(tk))
		// The first tick after start covers a partial second, so stop at the
		// tick nearest the target rather than the first one past it.
		if *duration > 0 && !stopping && tk.ElapsedS+0.5 >= duration.Seconds() {
			stopping = true
			_ = eng.Do(ctx, engine.Command{Kind: engine.CmdStop})
		}
	}
	err := <-runErr
	if started {
		fmt.Fprintf(stderr, "bike returned to level %d\n", lo)
		lastLevel.Store(int32(lo))
	}

	end := eng.Snapshot()
	avg := 0.0
	if end.ElapsedS > 0 {
		avg = end.DistanceM / end.ElapsedS * 3.6
	}
	summary := func(label string) {
		fmt.Fprintf(stdout, "%s: %s %.3f km avg %.1f km/h\n", label, clock(end.ElapsedS), end.DistanceM/1000, avg)
	}
	switch {
	case err == nil:
		summary("ride finished")
		return 0
	case interrupted(ctx):
		summary("ride interrupted")
		return exitInterrupted
	}
	fmt.Fprintf(stderr, "wattson ride: %v\n", err)
	return 1
}
