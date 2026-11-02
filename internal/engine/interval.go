package engine

// IntervalMode rides a timeline of fixed-level blocks. The rider's level bias
// (level_delta) shifts every block's level.
type IntervalMode struct {
	id     int64
	blocks []WorkoutBlock
	lo, hi int
}

// NewIntervalMode returns a mode that follows blocks; minLevel and maxLevel
// are the rider's bounds (the floor is never below 1).
func NewIntervalMode(id int64, blocks []WorkoutBlock, minLevel, maxLevel int) *IntervalMode {
	lo := max(minLevel, 1)
	return &IntervalMode{id: id, blocks: blocks, lo: lo, hi: max(maxLevel, lo)}
}

func (*IntervalMode) Name() string { return "interval" }

// UsesBias reports that level_delta adjusts a live bias in this mode.
func (*IntervalMode) UsesBias() bool { return true }

// WorkoutInfo returns the workout id and its block count.
func (m *IntervalMode) WorkoutInfo() (id int64, blocks int) { return m.id, len(m.blocks) }

func (m *IntervalMode) Step(in ModeInput) ModeOutput {
	i, left, done := blockAt(m.blocks, in.Elapsed)
	out := ModeOutput{Block: i, BlockLeft: left, Done: done, Level: m.lo}
	if len(m.blocks) > 0 {
		out.Level = clampInt(m.blocks[i].Level+in.Bias, m.lo, m.hi)
	}
	return out
}
