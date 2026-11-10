package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/metril/wattson/internal/store"
)

const (
	backfillBatch = 50
	backfillYield = 2 * time.Millisecond
)

// Backfill computes training metrics for finished rides recorded before the
// current store.StatsVersion. One ride is loaded and written per transaction,
// with a pause between rides, so memory stays bounded and live rides are not
// starved. Rides lacking snapshots use the rider's current values. It is a
// no-op once every ride is current and stops early when ctx is cancelled. It
// returns the number of rides updated.
func Backfill(ctx context.Context, st *store.Store, log *slog.Logger) (int, error) {
	var done int
	var after int64
	var errs []error
	for {
		rides, err := st.RidesNeedingStats(ctx, after, backfillBatch)
		if err != nil {
			return done, errors.Join(append(errs, err)...)
		}
		if len(rides) == 0 {
			return done, errors.Join(errs...)
		}
		for _, r := range rides {
			after = r.ID
			ok, err := backfillRide(ctx, st, r)
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			if err != nil {
				log.Warn("ride stats backfill failed", "ride", r.ID, "err", err)
				errs = append(errs, fmt.Errorf("ride %d: %w", r.ID, err))
			} else if ok {
				done++
			}
			select {
			case <-ctx.Done():
				return done, ctx.Err()
			case <-time.After(backfillYield):
			}
		}
	}
}

func backfillRide(ctx context.Context, st *store.Store, r store.Ride) (bool, error) {
	snap := store.Snapshot{FTPW: r.FTPW, HRMaxBpm: r.HRMaxBpm, WeightKg: r.WeightKg, RouteDistanceM: r.RouteDistanceM}
	if snap.FTPW == 0 || snap.WeightKg == 0 || snap.HRMaxBpm == 0 {
		rd, err := st.RiderByID(ctx, r.RiderID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
		if err == nil {
			if snap.FTPW == 0 {
				snap.FTPW = rd.FTPW
			}
			if snap.WeightKg == 0 {
				snap.WeightKg = rd.WeightKg
			}
			if snap.HRMaxBpm == 0 {
				snap.HRMaxBpm = rd.MaxHRBpm
			}
		}
	}
	if snap.RouteDistanceM == 0 && r.RouteID != 0 {
		d, err := st.RouteDistance(ctx, r.RouteID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
		snap.RouteDistanceM = d
	}
	ss, err := st.Samples(ctx, r.ID)
	if err != nil {
		return false, err
	}
	sum := Summarize(ss, Params{FTPW: snap.FTPW, MaxHRBpm: snap.HRMaxBpm, WeightKg: snap.WeightKg,
		RouteDistanceM: snap.RouteDistanceM})
	return st.ApplyRideStats(ctx, r.ID, sum, snap)
}
