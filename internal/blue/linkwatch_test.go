package blue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchLinkFiresOnFalse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	connected := func() (bool, error) { return calls.Add(1) <= 2, nil }
	down := make(chan struct{}, 2)
	finished := make(chan struct{})
	go func() {
		watchLink(context.Background(), 5*time.Millisecond, connected, func() { down <- struct{}{} })
		close(finished)
	}()
	select {
	case <-down:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("onDown not called within 50ms")
	}
	select {
	case <-finished:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("watchLink did not return after onDown")
	}
	if len(down) != 0 {
		t.Fatal("onDown called more than once")
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("connected polled %d times, want 3", n)
	}
}

func TestWatchLinkStopsOnCtx(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var downs atomic.Int32
	finished := make(chan struct{})
	go func() {
		watchLink(ctx, time.Millisecond, func() (bool, error) { return true, nil }, func() { downs.Add(1) })
		close(finished)
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("watchLink did not stop on ctx cancel")
	}
	if downs.Load() != 0 {
		t.Fatal("onDown must not fire on ctx cancel")
	}
}

func TestWatchLinkTreatsErrorAsDown(t *testing.T) {
	t.Parallel()
	down := make(chan struct{}, 2)
	go watchLink(context.Background(), 5*time.Millisecond,
		func() (bool, error) { return true, errors.New("dbus: gone") },
		func() { down <- struct{}{} })
	select {
	case <-down:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("onDown not called on poll error")
	}
}
