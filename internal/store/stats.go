package store

import (
	"context"
	"fmt"
	"time"

	"github.com/metril/wattson/internal/stats"
)

// riderScope is a SQL fragment selecting rides of one rider, or of all riders
// of one user, plus its argument. The caller must pass a non-zero id.
func riderScope(userID, riderID int64) (string, int64) {
	if riderID != 0 {
		return `r.rider_id = ?`, riderID
	}
	return `r.rider_id IN (SELECT id FROM riders WHERE user_id = ?)`, userID
}

// RideBuckets sums the finished rides of one rider (or of every rider of
// userID when riderID is 0) per 15-minute UTC slot, oldest first. The caller
// groups the slots into local days or periods. One indexed query; the
// aggregation happens in SQLite.
func (s *Store) RideBuckets(ctx context.Context, userID, riderID int64) ([]stats.Bucket, error) {
	scope, id := riderScope(userID, riderID)
	rows, err := s.Read.QueryContext(ctx,
		`SELECT MIN(r.started_at), COUNT(*), SUM(r.duration_s), SUM(r.distance_m), SUM(r.ascent_m),
			SUM(r.kj), SUM(COALESCE(r.tss, 0))
		 FROM rides r
		 WHERE r.ended_at IS NOT NULL AND `+scope+`
		 GROUP BY substr(r.started_at, 1, 13), CAST(substr(r.started_at, 15, 2) AS INTEGER) / 15
		 ORDER BY 1`, id)
	if err != nil {
		return nil, fmt.Errorf("ride buckets: %w", err)
	}
	defer rows.Close()
	var out []stats.Bucket
	for rows.Next() {
		var b stats.Bucket
		var at string
		if err := rows.Scan(&at, &b.Rides, &b.DurationS, &b.DistanceM, &b.AscentM, &b.KJ, &b.TSS); err != nil {
			return nil, fmt.Errorf("ride buckets: %w", err)
		}
		if b.At, err = time.Parse(TimeFormat, at); err != nil {
			return nil, fmt.Errorf("ride buckets: parse time: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BestPoint is the best power at one duration and the ride that set it.
type BestPoint struct {
	DurS      int
	Watts     float64
	RideID    int64
	StartedAt time.Time
	WeightKg  float64 // the ride's snapshot, else the rider's current weight
}

// BestPowerCurve returns, per duration, the best mean power over the finished
// rides started at or after since (zero = all time) of one rider or of every
// rider of userID. One query: MAX per duration over ride_power_curve joined to
// the rider's rides (rides_rider_started); SQLite's bare-column rule yields
// the ride that set each max, the first scanned (earliest) on ties.
func (s *Store) BestPowerCurve(ctx context.Context, userID, riderID int64, since time.Time) ([]BestPoint, error) {
	scope, id := riderScope(userID, riderID)
	from := ""
	if !since.IsZero() {
		from = fmtTime(since)
	}
	rows, err := s.Read.QueryContext(ctx,
		`SELECT b.dur_s, b.watts, b.ride_id, r.started_at, COALESCE(r.weight_kg, rd.weight_kg)
		 FROM (
			SELECT c.dur_s AS dur_s, MAX(c.watts) AS watts, c.ride_id AS ride_id
			FROM rides r
			JOIN ride_power_curve c ON c.ride_id = r.id
			WHERE r.ended_at IS NOT NULL AND r.started_at >= ? AND `+scope+`
			GROUP BY c.dur_s
		 ) b
		 JOIN rides r ON r.id = b.ride_id
		 JOIN riders rd ON rd.id = r.rider_id
		 ORDER BY b.dur_s`, from, id)
	if err != nil {
		return nil, fmt.Errorf("best power curve: %w", err)
	}
	defer rows.Close()
	out := make([]BestPoint, 0, len(stats.Durations))
	for rows.Next() {
		var p BestPoint
		var at string
		if err := rows.Scan(&p.DurS, &p.Watts, &p.RideID, &at, &p.WeightKg); err != nil {
			return nil, fmt.Errorf("best power curve: %w", err)
		}
		if p.StartedAt, err = time.Parse(TimeFormat, at); err != nil {
			return nil, fmt.Errorf("best power curve: parse time: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
