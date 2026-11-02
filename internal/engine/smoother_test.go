package engine

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func sec(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }

func TestSmootherInit(t *testing.T) {
	t.Parallel()
	s := DefaultSmoother(1, 16)
	if got := s.Step(5.4, 0); got != 5 {
		t.Fatalf("got %d", got)
	}
	s = DefaultSmoother(0, 16)
	if got := s.Step(-3, 0); got != 1 {
		t.Fatalf("min floor: got %d", got)
	}
}

func TestSmootherHysteresis(t *testing.T) {
	t.Parallel()
	s := DefaultSmoother(1, 16)
	s.Step(6, 0)
	for _, c := range []struct {
		target float64
		at     float64
		want   int
	}{{6.9, 3, 6}, {7.0, 4, 7}, {6.1, 7, 7}, {6.0, 10, 6}} {
		if got := s.Step(c.target, sec(c.at)); got != c.want {
			t.Errorf("Step(%v@%vs)=%d want %d", c.target, c.at, got, c.want)
		}
	}
}

func TestSmootherRateLimited(t *testing.T) {
	t.Parallel()
	s := DefaultSmoother(1, 16)
	s.Step(5, 0)
	var got []int
	for i := 1; i <= 8; i++ {
		got = append(got, s.Step(12, sec(float64(i))))
	}
	if want := []int{5, 7, 7, 9, 9, 11, 11, 12}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSmootherNoYoYo(t *testing.T) {
	t.Parallel()
	s := DefaultSmoother(1, 16)
	s.Step(6, 0)
	for i := 1; i <= 600; i++ {
		tm := sec(float64(i))
		if got := s.Step(6+0.45*math.Sin(float64(tm)/float64(3*time.Second)), tm); got != 6 {
			t.Fatalf("sine t=%d: got %d", i, got)
		}
	}
	r := rand.New(rand.NewPCG(1, 2))
	changes, prev := 0, 6
	for i := 601; i <= 1200; i++ {
		got := s.Step(6.1+0.8*r.Float64(), sec(float64(i)))
		if got != prev {
			changes++
		}
		prev = got
	}
	if changes > 1 {
		t.Fatalf("%d changes", changes)
	}
}

func FuzzSmootherBounds(f *testing.F) {
	f.Add(uint64(1))
	f.Add(uint64(42))
	f.Add(uint64(1 << 40))
	f.Fuzz(func(t *testing.T, seed uint64) {
		r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		s := DefaultSmoother(1, 16)
		prev, lastChange := 0, time.Duration(0)
		for i := 0; i < 300; i++ {
			now := sec(float64(i))
			got := s.Step(-5+35*r.Float64(), now)
			if got < 1 || got > 16 {
				t.Fatalf("i=%d level %d out of [1,16]", i, got)
			}
			if i > 0 && got != prev {
				if d := got - prev; d > 2 || d < -2 {
					t.Fatalf("i=%d step %d", i, d)
				}
				if now-lastChange < 2*time.Second {
					t.Fatalf("i=%d changed after %v", i, now-lastChange)
				}
			}
			if i == 0 || got != prev {
				lastChange = now
			}
			prev = got
		}
	})
}
