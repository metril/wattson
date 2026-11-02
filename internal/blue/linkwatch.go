package blue

import (
	"context"
	"time"
)

// watchLink polls connected every interval and calls onDown exactly once when
// the link reports down or the poll fails, then returns. It also returns
// (without calling onDown) when ctx is done. It blocks; run it in a goroutine.
// Polling stands in for a remote-disconnect callback, which the BlueZ central
// API does not provide.
func watchLink(ctx context.Context, interval time.Duration, connected func() (bool, error), onDown func()) {
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		if ok, err := connected(); err != nil || !ok {
			if ctx.Err() == nil {
				onDown()
			}
			return
		}
	}
}
