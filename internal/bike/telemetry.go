// Package bike holds pure, I/O-free codecs for the Merach S29R2: the FitPlus
// proprietary protocol, FTMS, protocol detection and the level safety clamp.
package bike

import (
	"errors"
	"time"
)

// ErrLevelRange means the requested resistance level cannot be encoded safely
// (level 0 can reboot the bike, so it is never encodable).
var ErrLevelRange = errors.New("bike: level out of range")

// ErrTruncated means a notification was shorter than its flags/format require.
var ErrTruncated = errors.New("bike: truncated frame")

// Telemetry is one decoded sample. At is NOT set by the parsers: the session
// (which owns the clock) stamps it on receipt, so parsers stay pure.
type Telemetry struct {
	Level    int
	Cadence  float64  // rpm
	PowerW   float64  // watts
	SpeedKmh *float64 // nil when the bike did not report speed
	At       time.Time
}
