package store

import (
	"context"
	"fmt"

	"github.com/metril/wattson/internal/stats"
)

// RidePowerCurve returns one ride's power curve, shortest duration first.
func (s *Store) RidePowerCurve(ctx context.Context, rideID int64) ([]stats.Point, error) {
	rows, err := s.Read.QueryContext(ctx,
		`SELECT dur_s, watts FROM ride_power_curve WHERE ride_id = ? ORDER BY dur_s`, rideID)
	if err != nil {
		return nil, fmt.Errorf("ride power curve: %w", err)
	}
	defer rows.Close()
	out := make([]stats.Point, 0, len(stats.Durations))
	for rows.Next() {
		var p stats.Point
		if err := rows.Scan(&p.DurS, &p.Watts); err != nil {
			return nil, fmt.Errorf("ride power curve: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
