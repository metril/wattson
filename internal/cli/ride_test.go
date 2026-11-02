package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/engine"
)

func TestFormatTick(t *testing.T) {
	t.Parallel()
	tk := engine.Tick{ElapsedS: 65, State: "riding", Level: 8, ReportedLevel: 8, CadenceRpm: 80, PowerW: 190, SpeedKmh: 31.24, DistanceM: 512}
	if got, want := formatTick(tk), "00:01:05 riding   L 8 ( 8)  80rpm  190W  31.2km/h   0.512km"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	tk.ReportedLevel = 0
	if got := formatTick(tk); !strings.Contains(got, "L 8 ( -)") {
		t.Fatalf("got %q, want ( -)", got)
	}
}

func TestRideFlagErrors(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"neither":    {"ride"},
		"both":       {"ride", "--sim", "--addr", testAddr.String()},
		"bad addr":   {"ride", "--addr", "bogus"},
		"bad proto":  {"ride", "--sim", "--proto", "x"},
		"bad max":    {"ride", "--sim", "--max", "0"},
		"extra args": {"ride", "--sim", "oops"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, _, errs := runCLI(t, testDeps(fitPlusEcho()), args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (%s)", code, errs)
			}
		})
	}
	_, _, errs := runCLI(t, testDeps(fitPlusEcho()), "ride")
	if !strings.Contains(errs, "wattson ride: need exactly one of --sim or --addr") {
		t.Fatalf("stderr %q", errs)
	}
}

// capture records the sim sessions a ride dials.
type capture struct {
	mu   sync.Mutex
	sess []*sim.Session
}

func (c *capture) dial(cfg sim.Config) bike.Dial {
	return func(context.Context) (bike.Session, error) {
		s := sim.New(cfg)
		c.mu.Lock()
		c.sess = append(c.sess, s)
		c.mu.Unlock()
		return s, nil
	}
}

func (c *capture) last() *sim.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess[len(c.sess)-1]
}

func lastLevelOf(s *sim.Session) int {
	l := s.Levels()
	return l[len(l)-1]
}

func TestRideSim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := &capture{}
		d := testDeps(nil)
		d.simDial = c.dial
		code, out, errs := runCLI(t, d, "ride", "--sim", "--duration", "5s", "--level", "8", "--cadence", "80")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "00:00:05 riding   L 8 ( 8)  80rpm  190W") }) {
			t.Fatalf("no 5 s riding line:\n%s", out)
		}
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "ride finished: 00:00:05") {
			t.Fatalf("last line %q", last)
		}
		if got := lastLevelOf(c.last()); got != 1 {
			t.Fatalf("last level %d, want 1 (levels %v)", got, c.last().Levels())
		}
		if !strings.Contains(errs, "bike returned to level 1") {
			t.Fatalf("stderr %q", errs)
		}
	})
}

func TestRideFitPlusFake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := fitPlusEcho()
		code, out, errs := runCLI(t, testDeps(f), "ride", "--addr", testAddr.String(), "--duration", "3s", "--level", "6")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errs)
		}
		w := levelWrites(f)
		if !slices.Contains(w, 6) || len(w) == 0 || w[len(w)-1] != 1 {
			t.Fatalf("level writes %v, want 6 ... 1", w)
		}
		if slices.Contains(w, 0) {
			t.Fatalf("level 0 written: %v", w)
		}
		if !strings.Contains(out, "ride finished: 00:00:03") {
			t.Fatalf("stdout:\n%s", out)
		}
	})
}

func TestRideInterruptReturnsBikeToMin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := &capture{}
		d := testDeps(nil)
		d.simDial = c.dial
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(10*time.Second, cancel)
		var out, errb bytes.Buffer
		code := run(ctx, d, []string{"ride", "--sim", "--level", "9"}, &out, &errb)
		if code != 130 {
			t.Fatalf("exit %d, want 130: %s", code, errb.String())
		}
		if got := lastLevelOf(c.last()); got != 1 {
			t.Fatalf("last level %d, want 1 (levels %v)", got, c.last().Levels())
		}
		if !strings.Contains(errb.String(), "bike returned to level 1") {
			t.Fatalf("stderr %q", errb.String())
		}
		if !slices.Contains(c.last().Levels(), 9) {
			t.Fatalf("levels %v never reached 9", c.last().Levels())
		}
	})
}
