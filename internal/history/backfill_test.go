package history_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestBackfillIdempotent(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	ctx := t.Context()
	rd := seedRider(t, s)
	rd.MaxHRBpm = 190
	rd, err := s.UpdateRider(ctx, rd)
	if err != nil {
		t.Fatal(err)
	}
	open, err := s.CreateRide(ctx, store.Ride{RiderID: rd.ID, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	ss := steady(1500, 200, 140)
	if err := s.AppendSamples(ctx, open.ID, ss); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishRide(ctx, open.ID, t0.Add(time.Hour), store.Summary{DurationS: 1500}, len(ss)); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-migration ride: no snapshots, no stats.
	if _, err := s.Write.ExecContext(ctx, `UPDATE rides SET ftp_w=NULL, weight_kg=NULL, hr_max_bpm=NULL WHERE id=?`, open.ID); err != nil {
		t.Fatal(err)
	}
	// The rider's FTP changed since; the backfill uses the current value.
	rd.FTPW = 250
	if _, err := s.UpdateRider(ctx, rd); err != nil {
		t.Fatal(err)
	}

	n, err := history.Backfill(ctx, s, nopLogger())
	if err != nil || n != 1 {
		t.Fatalf("first backfill = %d, %v", n, err)
	}
	got, err := s.Ride(ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.StatsVersion != store.StatsVersion || got.FTPW != 250 || got.HRMaxBpm != 190 ||
		got.Summary.TSS == nil || got.Summary.DecouplingPct == nil {
		t.Fatalf("ride after backfill = %+v", got)
	}
	ex, err := s.RideExtras(ctx, []int64{open.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ex[open.ID].PeakW[300] != 200 || len(ex[open.ID].PowerZones) != 7 || len(ex[open.ID].HRZones) != 5 {
		t.Fatalf("extras = %+v", ex[open.ID])
	}
	var curve, zones int
	count := func() {
		if err := s.Read.QueryRow(`SELECT (SELECT count(*) FROM ride_power_curve), (SELECT count(*) FROM ride_zones)`).Scan(&curve, &zones); err != nil {
			t.Fatal(err)
		}
	}
	count()
	c1, z1 := curve, zones
	n, err = history.Backfill(ctx, s, nopLogger())
	if err != nil || n != 0 {
		t.Fatalf("second backfill = %d, %v", n, err)
	}
	count()
	if curve != c1 || zones != z1 || c1 == 0 || z1 == 0 {
		t.Errorf("rows changed or empty: curve %d->%d zones %d->%d", c1, curve, z1, zones)
	}
}

func TestBackfillCancelled(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	rd := seedRider(t, s)
	r := openRide(t, s, rd.ID, 20)
	if _, err := s.FinishRide(t.Context(), r.ID, t0.Add(time.Minute), store.Summary{}, 20); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context_WithCancel(t)
	cancel()
	if n, err := history.Backfill(ctx, s, nopLogger()); n != 0 || err == nil {
		t.Errorf("cancelled backfill = %d, %v", n, err)
	}
}

func nopLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func context_WithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithCancel(t.Context())
}
