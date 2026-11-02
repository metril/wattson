package engine

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func ivBlocks() []WorkoutBlock {
	return []WorkoutBlock{
		{Duration: 60 * time.Second, Level: 5},
		{Duration: 30 * time.Second, Level: 10},
		{Duration: 60 * time.Second, Level: 3},
	}
}

// ivRide starts an interval ride (id 4) and returns the engine, driver and
// tick subscription.
func ivRide(t *testing.T, blocks []WorkoutBlock) (*Engine, *fakeDriver, <-chan Tick, chan error, context.CancelFunc) {
	t.Helper()
	f := newFake()
	e := New(Config{Driver: f})
	sub, cancelSub := e.Subscribe(256)
	res, stop := start(t, e)
	startWith(t, e, f, NewIntervalMode(4, blocks, 1, 16))
	return e, f, sub, res, func() { stop(); cancelSub() }
}

func TestIntervalTransitionsExact(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, f, sub, res, _ := ivRide(t, ivBlocks())
		ticks := advance(150*time.Second, sub)
		at := func(s int) Tick {
			for _, tk := range ticks {
				if tk.ElapsedS == float64(s) && tk.State == StateRiding || (tk.State == StateFinished && tk.ElapsedS == float64(s)) {
					return tk
				}
			}
			t.Fatalf("no tick at %d s", s)
			return Tick{}
		}
		for _, c := range []struct {
			s, idx, level int
			left          float64
		}{{59, 0, 5, 1}, {60, 1, 10, 30}, {90, 2, 3, 60}} {
			tk := at(c.s)
			if tk.BlockIndex != c.idx || tk.BlockRemainingS != c.left || tk.Level != c.level || tk.Mode != "interval" || tk.WorkoutID != 4 || tk.BlockCount != 3 {
				t.Errorf("%d s: %+v, want block %d left %v level %d", c.s, tk, c.idx, c.left, c.level)
			}
		}
		if last := lastTick(t, ticks); last.State != StateFinished {
			t.Fatalf("last tick %+v, want finished", last)
		}
		if err := <-res; err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got, want := f.Calls(), []int{5, 10, 3, 1}; !slices.Equal(got, want) {
			t.Fatalf("driver calls %v, want %v", got, want)
		}
	})
}

func TestIntervalPauseFreezesBlock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, f, sub, _, stop := ivRide(t, ivBlocks())
		defer stop()
		advance(30*time.Second, sub)
		if err := do(t, e, "pause", 0); err != nil {
			t.Fatal(err)
		}
		advance(120*time.Second, sub)
		if err := do(t, e, "resume", 0); err != nil {
			t.Fatal(err)
		}
		tk := lastTick(t, advance(time.Second, sub))
		if tk.State != StateRiding || tk.BlockIndex != 0 || tk.BlockRemainingS != 29 || tk.Level != 5 {
			t.Fatalf("after resume: %+v, want block 0 left 29 level 5", tk)
		}
		if got, want := f.Calls(), []int{5, 1, 5}; !slices.Equal(got, want) {
			t.Fatalf("driver calls %v, want %v", got, want)
		}
	})
}

func TestIntervalBias(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, _, sub, _, stop := ivRide(t, ivBlocks())
		defer stop()
		advance(time.Second, sub)
		if err := do(t, e, "level_delta", 2); err != nil {
			t.Fatal(err)
		}
		if tk := lastTick(t, advance(time.Second, sub)); tk.Level != 7 || tk.Bias != 2 {
			t.Fatalf("block 0: %+v, want level 7", tk)
		}
		advance(60*time.Second, sub) // into block 1
		if tk := lastTick(t, advance(time.Second, sub)); tk.BlockIndex != 1 || tk.Level != 12 {
			t.Fatalf("block 1: %+v, want level 12", tk)
		}
		if err := do(t, e, "level_delta", -20); err != nil {
			t.Fatal(err)
		}
		if tk := lastTick(t, advance(time.Second, sub)); tk.Bias != -MaxBias || tk.Level != 1 {
			t.Fatalf("bias -10: %+v, want level 1", tk)
		}
	})
}

func TestIntervalClampsOutOfRange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, _, sub, _, stop := ivRide(t, []WorkoutBlock{{Duration: 10 * time.Second, Level: 40}})
		defer stop()
		if tk := lastTick(t, advance(time.Second, sub)); tk.Level != 16 {
			t.Fatalf("level %d, want 16", tk.Level)
		}
	})
}
