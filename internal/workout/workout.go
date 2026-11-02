// Package workout defines the stored workout block schema, validates it and
// resolves its blocks against a rider into engine timeline blocks.
package workout

import (
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/store"
)

// Type is the kind of workout: interval blocks set a resistance level, ERG
// blocks hold a target power.
type Type string

const (
	Interval Type = "interval"
	Erg      Type = "erg"
)

// Block is one step of a workout. Interval blocks carry Level or
// LevelOffset; ERG blocks carry PowerW or FTPPct.
type Block struct {
	DurationS   int    `json:"duration_s"`
	Level       *int   `json:"level,omitempty"`
	LevelOffset *int   `json:"level_offset,omitempty"`
	PowerW      *int   `json:"power_w,omitempty"`
	FTPPct      *int   `json:"ftp_pct,omitempty"`
	Cadence     string `json:"cadence,omitempty"`
	Label       string `json:"label,omitempty"`
}

// Limits.
const (
	MaxBlocks = 200
	MinBlockS = 5
	MaxBlockS = 7200
	MaxTotalS = 21600
)

const (
	maxCadenceLen = 32
	maxLabelLen   = 40
)

// ValidationError names the offending field, e.g. "blocks[2].power_w".
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func bad(field, format string, a ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// DurationS is the total length of bs in seconds.
func DurationS(bs []Block) int {
	var n int
	for _, b := range bs {
		n += b.DurationS
	}
	return n
}

func inRange(p *int, lo, hi int) bool { return *p >= lo && *p <= hi }

// Validate checks a workout of type t.
func Validate(t Type, bs []Block) error {
	if t != Interval && t != Erg {
		return bad("type", "must be interval or erg")
	}
	if len(bs) < 1 || len(bs) > MaxBlocks {
		return bad("blocks", "must have 1 to %d blocks", MaxBlocks)
	}
	total := 0
	for i, b := range bs {
		f := func(name string) string { return fmt.Sprintf("blocks[%d].%s", i, name) }
		if b.DurationS < MinBlockS || b.DurationS > MaxBlockS {
			return bad(f("duration_s"), "must be %d to %d seconds", MinBlockS, MaxBlockS)
		}
		total += b.DurationS
		switch t {
		case Interval:
			if b.PowerW != nil {
				return bad(f("power_w"), "not allowed for interval")
			}
			if b.FTPPct != nil {
				return bad(f("ftp_pct"), "not allowed for interval")
			}
			if (b.Level == nil) == (b.LevelOffset == nil) {
				return bad(f("level"), "exactly one of level or level_offset required for interval")
			}
			if b.Level != nil && !inRange(b.Level, 1, 32) {
				return bad(f("level"), "must be 1 to 32")
			}
			if b.LevelOffset != nil && !inRange(b.LevelOffset, -31, 31) {
				return bad(f("level_offset"), "must be -31 to 31")
			}
		case Erg:
			if b.Level != nil {
				return bad(f("level"), "not allowed for erg")
			}
			if b.LevelOffset != nil {
				return bad(f("level_offset"), "not allowed for erg")
			}
			if (b.PowerW == nil) == (b.FTPPct == nil) {
				return bad(f("power_w"), "exactly one of power_w or ftp_pct required for erg")
			}
			if b.PowerW != nil && !inRange(b.PowerW, engine.MinTargetW, engine.MaxTargetW) {
				return bad(f("power_w"), "must be %d to %d watts", engine.MinTargetW, engine.MaxTargetW)
			}
			if b.FTPPct != nil && !inRange(b.FTPPct, 20, 200) {
				return bad(f("ftp_pct"), "must be 20 to 200 percent")
			}
		}
		if utf8.RuneCountInString(b.Cadence) > maxCadenceLen {
			return bad(f("cadence"), "at most %d characters", maxCadenceLen)
		}
		if utf8.RuneCountInString(b.Label) > maxLabelLen {
			return bad(f("label"), "at most %d characters", maxLabelLen)
		}
	}
	if total > MaxTotalS {
		return bad("blocks", "total duration %d s exceeds %d s", total, MaxTotalS)
	}
	return nil
}

// Resolve turns validated blocks into engine timeline blocks for rider r.
// Interval levels are clamped to [max(1, r.MinLevel), r.MaxLevel] so a
// resolved workout never asks for level 0. ERG targets are clamped to the
// engine's accepted range (engine.MinTargetW..engine.MaxTargetW), so a rider
// with no FTP or a very high one still gets a target the engine accepts.
func Resolve(t Type, bs []Block, r store.Rider) []engine.WorkoutBlock {
	lo := max(1, r.MinLevel)
	hi := max(lo, r.MaxLevel)
	out := make([]engine.WorkoutBlock, len(bs))
	for i, b := range bs {
		wb := engine.WorkoutBlock{Duration: time.Duration(b.DurationS) * time.Second}
		switch t {
		case Interval:
			switch {
			case b.Level != nil:
				wb.Level = *b.Level
			case b.LevelOffset != nil:
				wb.Level = int(math.Round(r.OffsetLevel)) + *b.LevelOffset
			}
			wb.Level = min(max(wb.Level, lo), hi)
		case Erg:
			switch {
			case b.PowerW != nil:
				wb.TargetW = float64(*b.PowerW)
			case b.FTPPct != nil:
				wb.TargetW = math.Round(float64(*b.FTPPct) * float64(r.FTPW) / 100)
			}
			wb.TargetW = math.Max(engine.MinTargetW, math.Min(engine.MaxTargetW, wb.TargetW))
		}
		out[i] = wb
	}
	return out
}
