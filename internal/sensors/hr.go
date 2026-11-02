// Package sensors supplies optional rider sensors. A sensor failing never
// affects the bike or the ride.
package sensors

import (
	"context"
	"time"
)

// HRStatus describes a heart rate source for diagnostics.
type HRStatus struct {
	Source    string    `json:"source" doc:"Heart rate source: ble (Bluetooth monitor) or ha (Home Assistant entity)."`
	Target    string    `json:"target" doc:"What the source reads: a Bluetooth address with optional monitor name, or a Home Assistant entity id."`
	Connected bool      `json:"connected" doc:"True while the source is delivering or able to deliver readings."`
	BPM       int       `json:"bpm" doc:"Last reading in beats per minute; 0 when there has been none."`
	LastAt    time.Time `json:"last_at" doc:"Time of the last reading; zero when there has been none."`
	Error     string    `json:"error,omitempty" doc:"Last error from the source; absent when it is healthy."`
}

// Monitor is a heart rate source.
type Monitor interface {
	// Run keeps the source connected until ctx is done and returns ctx.Err() only.
	Run(ctx context.Context) error
	// Latest returns the last reading; 0 and the zero time before any reading.
	// It is lock-free.
	Latest() (bpm int, at time.Time)
	Status() HRStatus
}

// backoffLoop calls attempt until ctx is done, waiting min after the first
// failure, doubling up to max, and starting over at min after an attempt that
// delivered at least one reading. It returns ctx.Err().
func backoffLoop(ctx context.Context, attempt func(ctx context.Context) (gotReading bool, err error), min, max time.Duration) error {
	delay := min
	for ctx.Err() == nil {
		got, _ := attempt(ctx)
		if got {
			delay = min
		}
		if ctx.Err() != nil {
			break
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
		if delay *= 2; delay > max {
			delay = max
		}
	}
	return ctx.Err()
}
