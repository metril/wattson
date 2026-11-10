package store

import (
	"context"
	"fmt"
	"time"

	"github.com/metril/wattson/internal/stats"
)

// riderScope is the SQL filter and argument selecting one rider, or all of a
// user's riders when riderID is 0.
func riderScope(riderID, userID int64) (string, any) {
	if riderID != 0 {
		return `r.rider_id=?`, riderID
	}
	return `r.rider_id IN (SELECT id FROM riders WHERE user_id=?)`, userID
}

// RouteAttempts returns the finished rides on a route by one rider (riderID)
// or by all of a user's riders (riderID 0), oldest first.
func (s *Store) RouteAttempts(ctx context.Context, routeID, riderID, userID int64) ([]stats.Attempt, error) {
	where, arg := riderScope(riderID, userID)
	rows, err := s.Read.QueryContext(ctx,
		`SELECT r.id, r.started_at, r.elapsed_s, r.avg_speed_kmh, r.np_w, r.avg_power_w, r.avg_hr_bpm, r.completed
		 FROM rides r WHERE `+where+` AND r.route_id=? AND r.ended_at IS NOT NULL ORDER BY r.started_at, r.id`,
		arg, routeID)
	if err != nil {
		return nil, fmt.Errorf("route attempts: %w", err)
	}
	defer rows.Close()
	var out []stats.Attempt
	for rows.Next() {
		var a stats.Attempt
		var started string
		if err := rows.Scan(&a.RideID, &started, &a.ElapsedS, &a.AvgSpeedKmh, &a.NPW, &a.AvgPowerW, &a.AvgHRBpm,
			&a.Completed); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		if a.StartedAt, err = time.Parse(TimeFormat, started); err != nil {
			return nil, fmt.Errorf("parse started_at: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RoutePRTrack returns the (distance, elapsed) samples of the fastest
// completed ride on the route, in ride order, in one query. Both slices are
// empty when there is no completed ride.
func (s *Store) RoutePRTrack(ctx context.Context, routeID, riderID, userID int64) (dist, elapsed []float64, err error) {
	where, arg := riderScope(riderID, userID)
	rows, err := s.Read.QueryContext(ctx,
		`SELECT distance_m, elapsed_s FROM samples WHERE ride_id=(
			SELECT r.id FROM rides r WHERE `+where+` AND r.route_id=? AND r.completed=1 AND r.ended_at IS NOT NULL
			AND r.elapsed_s>0 ORDER BY r.elapsed_s, r.started_at, r.id LIMIT 1) ORDER BY seq`,
		arg, routeID)
	if err != nil {
		return nil, nil, fmt.Errorf("pr track: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d, e float64
		if err := rows.Scan(&d, &e); err != nil {
			return nil, nil, fmt.Errorf("scan pr track: %w", err)
		}
		dist, elapsed = append(dist, d), append(elapsed, e)
	}
	return dist, elapsed, rows.Err()
}
