package sim_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
)

func TestPowerModel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		level int
		rpm   float64
		want  float64
	}{{1, 80, 50}, {16, 80, 350}, {8, 85, 201.875}, {5, 0, 0}, {0, 80, 50}} {
		if got := sim.Power(c.level, c.rpm); got != c.want {
			t.Errorf("Power(%d,%v)=%v want %v", c.level, c.rpm, got, c.want)
		}
	}
}

func drain(ch <-chan bike.Telemetry) []bike.Telemetry {
	var out []bike.Telemetry
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

func TestEmitsEachPeriod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := sim.New(sim.Config{})
		defer s.Close(context.Background())
		if _, err := s.SetLevel(context.Background(), 8); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		got := drain(s.Telemetry())
		if len(got) != 3 {
			t.Fatalf("got %d samples, want 3", len(got))
		}
		for _, g := range got {
			if g.Level != 8 || g.Cadence != 80 || g.PowerW != 190 {
				t.Errorf("sample %+v", g)
			}
		}
	})
}

func TestScriptedCadence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := sim.New(sim.Config{Cadence: func(e time.Duration) float64 {
			if e < 2*time.Second {
				return 60
			}
			return 90
		}})
		defer s.Close(context.Background())
		time.Sleep(3 * time.Second)
		synctest.Wait()
		var cad []float64
		for _, g := range drain(s.Telemetry()) {
			cad = append(cad, g.Cadence)
		}
		if !slices.Equal(cad, []float64{60, 90, 90}) {
			t.Fatalf("cadence %v", cad)
		}
	})
}

func TestNeverBelowOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := sim.New(sim.Config{})
		if got, _ := s.SetLevel(ctx, 0); got != 1 {
			t.Errorf("SetLevel(0)=%d", got)
		}
		if got, _ := s.Resend(ctx, -3); got != 1 {
			t.Errorf("Resend(-3)=%d", got)
		}
		if got, _ := s.SetLevel(ctx, 99); got != 16 {
			t.Errorf("SetLevel(99)=%d", got)
		}
		s.Close(ctx)
		for _, l := range s.Levels() {
			if l < 1 || l > 16 {
				t.Errorf("level %d out of range", l)
			}
		}
	})
}

func TestSetLevelDedupsResendWrites(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := sim.New(sim.Config{})
		s.SetLevel(ctx, 5)
		s.SetLevel(ctx, 5)
		s.Resend(ctx, 5)
		s.Close(ctx)
		if got, want := s.Levels(), []int{5, 5, 1}; !slices.Equal(got, want) {
			t.Fatalf("levels %v want %v", got, want)
		}
	})
}

func TestDropAndClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := sim.New(sim.Config{})
		s.Drop()
		select {
		case <-s.Done():
		default:
			t.Fatal("Done not closed")
		}
		if !errors.Is(s.Err(), blue.ErrDisconnected) {
			t.Fatalf("Err=%v", s.Err())
		}
		synctest.Wait()
		for range s.Telemetry() { // must terminate: channel closed
		}
		if _, err := s.SetLevel(ctx, 3); !errors.Is(err, blue.ErrDisconnected) {
			t.Fatalf("SetLevel after drop: %v", err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}

		s2 := sim.New(sim.Config{})
		s2.SetLevel(ctx, 9)
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		l := s2.Levels()
		if l[len(l)-1] != 1 || s2.Err() != nil {
			t.Fatalf("levels %v err %v", l, s2.Err())
		}
		select {
		case <-s2.Done():
		default:
			t.Fatal("Done not closed after Close")
		}
		for range s2.Telemetry() {
		}
		if _, err := s2.SetLevel(ctx, 4); !errors.Is(err, bike.ErrClosed) {
			t.Fatalf("SetLevel after close: %v", err)
		}
	})
}

func TestSupervisorOverSim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sup := bike.NewSupervisor(sim.Dial(sim.Config{}), bike.SupervisorOptions{})
		if err := sup.Connect(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := sup.SetLevel(ctx, 6); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(2 * time.Second)
		for {
			select {
			case tel := <-sup.Telemetry():
				if tel.Level == 6 {
					sup.Close(ctx)
					return
				}
			case <-deadline:
				sup.Close(ctx)
				t.Fatal("no Level 6 telemetry within 2s")
			}
		}
	})
}

func TestCloseAppliesMinLevel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct{ min, want int }{{0, 1}, {3, 3}, {99, 16}} {
			s := sim.New(sim.Config{MinLevel: tc.min})
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if l := s.Levels(); l[len(l)-1] != tc.want {
				t.Fatalf("min %d: levels %v want last %d", tc.min, l, tc.want)
			}
		}
	})
}
