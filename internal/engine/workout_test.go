package engine

import (
	"testing"
	"time"
)

func TestBlockAt(t *testing.T) {
	t.Parallel()
	bs := []WorkoutBlock{{Duration: 60 * time.Second}, {Duration: 30 * time.Second}}
	s := time.Second
	for _, c := range []struct {
		elapsed time.Duration
		i       int
		left    time.Duration
		done    bool
	}{
		{0, 0, 60 * s, false},
		{59*s + 500*time.Millisecond, 0, 500 * time.Millisecond, false},
		{60 * s, 1, 30 * s, false},
		{90 * s, 1, 0, true},
		{200 * s, 1, 0, true},
	} {
		i, left, done := blockAt(bs, c.elapsed)
		if i != c.i || left != c.left || done != c.done {
			t.Errorf("elapsed %v: got (%d, %v, %v), want (%d, %v, %v)", c.elapsed, i, left, done, c.i, c.left, c.done)
		}
	}
	if _, _, done := blockAt(nil, 0); !done {
		t.Error("empty timeline should be done")
	}
}
