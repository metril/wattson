package stats

import "time"

// Bucket is a sum of finished rides that started within one 15-minute UTC
// slot. Fifteen minutes divides every real UTC offset, so a bucket never
// straddles a local midnight.
type Bucket struct {
	At                                     time.Time
	Rides                                  int
	DurationS, DistanceM, AscentM, KJ, TSS float64
}

// Total is the sum over one calendar period. Start is the period's first
// local day, as YYYY-MM-DD.
type Total struct {
	Start                                  string
	Rides                                  int
	DurationS, DistanceM, AscentM, KJ, TSS float64
}

// periodLimit is how many periods Totals returns per granularity.
var periodLimit = map[string]int{"week": 26, "month": 24, "year": 10}

// ValidPeriod reports whether p is week, month or year.
func ValidPeriod(p string) bool { _, ok := periodLimit[p]; return ok }

const secPerDay = 86400

// DayOrd is the number of civil days from 1970-01-01 to t's date in loc.
func DayOrd(t time.Time, loc *time.Location) int {
	y, m, d := t.In(loc).Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / secPerDay)
}

func ordDate(ord int) time.Time { return time.Unix(int64(ord)*secPerDay, 0).UTC() }

// DayString formats a day ordinal as YYYY-MM-DD.
func DayString(ord int) string { return ordDate(ord).Format(time.DateOnly) }

// WeekOrd is the Monday-based ISO week index of a day ordinal (1970-01-01 was a Thursday).
func WeekOrd(day int) int { return floorDiv(day+3, 7) }

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// periodStart returns the first civil day of the period holding day.
func periodStart(day int, period string) time.Time {
	d := ordDate(day)
	switch period {
	case "week":
		return ordDate(WeekOrd(day)*7 - 3)
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(d.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
}

func nextPeriod(t time.Time, period string) time.Time {
	switch period {
	case "week":
		return t.AddDate(0, 0, 7)
	case "month":
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(1, 0, 0)
	}
}

// Totals sums buckets into calendar periods in loc ("week" is an ISO week,
// Monday first). The result is oldest first, zero-filled, ends at the period
// containing now and is capped to the most recent periods.
func Totals(bs []Bucket, period string, loc *time.Location, now time.Time) []Total {
	limit, ok := periodLimit[period]
	if !ok {
		return nil
	}
	cur := periodStart(DayOrd(now, loc), period)
	sums := make(map[time.Time]*Total, len(bs))
	first := cur
	for _, b := range bs {
		ps := periodStart(DayOrd(b.At, loc), period)
		if ps.After(cur) { // after now: ignore
			continue
		}
		if ps.Before(first) {
			first = ps
		}
		t := sums[ps]
		if t == nil {
			t = &Total{Start: ps.Format(time.DateOnly)}
			sums[ps] = t
		}
		t.Rides += b.Rides
		t.DurationS += b.DurationS
		t.DistanceM += b.DistanceM
		t.AscentM += b.AscentM
		t.KJ += b.KJ
		t.TSS += b.TSS
	}
	out := make([]Total, 0, limit)
	for p := first; !p.After(cur); p = nextPeriod(p, period) {
		if t := sums[p]; t != nil {
			out = append(out, *t)
		} else {
			out = append(out, Total{Start: p.Format(time.DateOnly)})
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// StreakResult counts consecutive days, and consecutive ISO weeks, with a ride.
type StreakResult struct {
	CurrentDays, LongestDays, CurrentWeeks, LongestWeeks int
}

// Streaks takes the sorted, de-duplicated day ordinals that have a ride. A
// current streak is still alive when its last day is today or yesterday (its
// last week this or last week), so it does not reset before the day is over.
func Streaks(days []int, today int) StreakResult {
	var r StreakResult
	r.CurrentDays, r.LongestDays = run(days, today)
	weeks := make([]int, 0, len(days))
	for _, d := range days {
		if w := WeekOrd(d); len(weeks) == 0 || weeks[len(weeks)-1] != w {
			weeks = append(weeks, w)
		}
	}
	r.CurrentWeeks, r.LongestWeeks = run(weeks, WeekOrd(today))
	return r
}

// run returns the current and longest runs of consecutive values in xs
// (ascending, unique). The current run counts only if it ends at now or now-1.
func run(xs []int, now int) (cur, longest int) {
	n := 0
	for i, x := range xs {
		if i > 0 && x == xs[i-1]+1 {
			n++
		} else {
			n = 1
		}
		longest = max(longest, n)
	}
	if len(xs) > 0 && xs[len(xs)-1] >= now-1 {
		cur = n
	}
	return cur, longest
}
