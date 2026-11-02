package bike

import (
	"context"
	"time"
)

// Session is one live link to a bike (or simulator). *Bike and sim.Session
// implement it.
type Session interface {
	Protocol() Protocol
	MaxLevel() int
	// SetLevel clamps level and skips the write when it equals the last one sent.
	SetLevel(ctx context.Context, level int) (int, error)
	// Resend clamps level and always writes it.
	Resend(ctx context.Context, level int) (int, error)
	// Telemetry is closed when the session ends.
	Telemetry() <-chan Telemetry
	Done() <-chan struct{}
	Err() error
	// Close sends the floor level first and is idempotent.
	Close(ctx context.Context) error
}

var _ Session = (*Bike)(nil)

// Dial opens a new Session.
type Dial func(ctx context.Context) (Session, error)

// Driver supervises a bike across reconnects.
type Driver interface {
	// Connect starts supervision and does not block.
	Connect(ctx context.Context) error
	// SetLevel returns the clamped target; it is applied asynchronously.
	SetLevel(ctx context.Context, level int) (int, error)
	// Telemetry persists across reconnects.
	Telemetry() <-chan Telemetry
	Events() <-chan Event
	Caps() Caps
	Close(ctx context.Context) error
}

// Caps describes the driver's current capabilities.
type Caps struct {
	Protocol           Protocol
	MinLevel, MaxLevel int
	Connected          bool
}

type EventKind string

const (
	EventConnected        EventKind = "connected"
	EventDisconnected     EventKind = "disconnected"
	EventLevelUnconfirmed EventKind = "level_unconfirmed"
	EventWatchdog         EventKind = "watchdog"
)

// Event reports a driver state change. Want/Got are levels for
// EventLevelUnconfirmed.
type Event struct {
	Kind      EventKind
	At        time.Time
	Want, Got int
	Err       error
}
