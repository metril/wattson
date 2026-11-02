package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// Summary holds the aggregate figures of a finished ride. Zero HR = none.
type Summary struct {
	DurationS, ElapsedS, DistanceM, AscentM float64
	AvgPowerW, MaxPowerW, NPW, KJ           float64
	AvgCadenceRpm, AvgSpeedKmh, MaxSpeedKmh float64
	AvgHRBpm, MaxHRBpm                      int
}

// StravaState is a ride's Strava upload progress.
type StravaState struct {
	Status               string // none|uploading|done|error
	UploadID, ActivityID int64
	Error                string
	UpdatedAt            time.Time
}

// Ride is a recorded ride. UserID and RiderName come from the rider join.
type Ride struct {
	ID, RiderID, UserID int64
	RiderName           string
	RouteID             int64 // 0 = free or deleted
	RouteName, Mode     string
	StartedAt           time.Time
	EndedAt             *time.Time
	Summary             Summary
	SampleCount         int
	Strava              StravaState
}

// Sample is one 1 Hz ride sample. Lat, Lon and EleM are NaN when absent
// (NULL in the database); HRBpm 0 means no reading.
type Sample struct {
	Seq                                                         int
	At                                                          time.Time
	ElapsedS, DistanceM, SpeedKmh, PowerW, CadenceRpm, GradePct float64
	Level, HRBpm                                                int
	Lat, Lon, EleM                                              float64
}

// RideQuery filters ListRides. Zero UserID/RiderID = all; Before 0 = newest.
type RideQuery struct {
	UserID, RiderID, Before int64
	Limit                   int
}

const rideCols = `r.id, r.rider_id, rd.user_id, rd.name, r.route_id, r.route_name, r.mode,
	r.started_at, r.ended_at, r.duration_s, r.elapsed_s, r.distance_m, r.ascent_m, r.avg_power_w,
	r.max_power_w, r.np_w, r.kj, r.avg_cadence_rpm, r.avg_speed_kmh, r.max_speed_kmh,
	r.avg_hr_bpm, r.max_hr_bpm, r.sample_count, r.strava_status, r.strava_upload_id,
	r.strava_activity_id, r.strava_error, r.strava_updated_at`

const rideFrom = ` FROM rides r JOIN riders rd ON rd.id = r.rider_id `

func scanRide(r scanner) (Ride, error) {
	var x Ride
	var route, upload, activity sql.NullInt64
	var started string
	var ended, supd sql.NullString
	s := &x.Summary
	if err := r.Scan(&x.ID, &x.RiderID, &x.UserID, &x.RiderName, &route, &x.RouteName, &x.Mode,
		&started, &ended, &s.DurationS, &s.ElapsedS, &s.DistanceM, &s.AscentM, &s.AvgPowerW,
		&s.MaxPowerW, &s.NPW, &s.KJ, &s.AvgCadenceRpm, &s.AvgSpeedKmh, &s.MaxSpeedKmh,
		&s.AvgHRBpm, &s.MaxHRBpm, &x.SampleCount, &x.Strava.Status, &upload, &activity,
		&x.Strava.Error, &supd); err != nil {
		return Ride{}, err
	}
	x.RouteID, x.Strava.UploadID, x.Strava.ActivityID = route.Int64, upload.Int64, activity.Int64
	var err error
	if x.StartedAt, err = time.Parse(TimeFormat, started); err != nil {
		return Ride{}, fmt.Errorf("parse started_at: %w", err)
	}
	if ended.Valid {
		t, err := time.Parse(TimeFormat, ended.String)
		if err != nil {
			return Ride{}, fmt.Errorf("parse ended_at: %w", err)
		}
		x.EndedAt = &t
	}
	if supd.Valid {
		if x.Strava.UpdatedAt, err = time.Parse(TimeFormat, supd.String); err != nil {
			return Ride{}, fmt.Errorf("parse strava_updated_at: %w", err)
		}
	}
	return x, nil
}

// CreateRide inserts an open ride (no end time) for r.RiderID.
func (s *Store) CreateRide(ctx context.Context, r Ride) (Ride, error) {
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO rides(rider_id, route_id, route_name, mode, started_at) VALUES(?,?,?,?,?)`,
		r.RiderID, nullInt(r.RouteID), r.RouteName, r.Mode, fmtTime(r.StartedAt))
	if err != nil {
		return Ride{}, fmt.Errorf("create ride: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Ride{}, fmt.Errorf("create ride: %w", err)
	}
	return s.Ride(ctx, id)
}

func nullF(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

// AppendSamples inserts ss in one transaction; rows already present
// (ride_id, seq) are ignored, so a retry is safe.
func (s *Store) AppendSamples(ctx context.Context, rideID int64, ss []Sample) error {
	if len(ss) == 0 {
		return nil
	}
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("append samples: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO samples(ride_id, seq, at_ms, elapsed_s, distance_m, speed_kmh, power_w,
			cadence_rpm, grade_pct, level, hr_bpm, lat, lon, ele_m) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("append samples: %w", err)
	}
	defer st.Close()
	for _, x := range ss {
		if _, err := st.ExecContext(ctx, rideID, x.Seq, x.At.UnixMilli(), x.ElapsedS, x.DistanceM,
			x.SpeedKmh, x.PowerW, x.CadenceRpm, x.GradePct, x.Level, nullInt(int64(x.HRBpm)),
			nullF(x.Lat), nullF(x.Lon), nullF(x.EleM)); err != nil {
			return fmt.Errorf("append samples: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("append samples: %w", err)
	}
	return nil
}

// Samples returns the ride's samples in seq order.
func (s *Store) Samples(ctx context.Context, rideID int64) ([]Sample, error) {
	rows, err := s.Read.QueryContext(ctx,
		`SELECT seq, at_ms, elapsed_s, distance_m, speed_kmh, power_w, cadence_rpm, grade_pct, level,
			hr_bpm, lat, lon, ele_m FROM samples WHERE ride_id=? ORDER BY seq`, rideID)
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var x Sample
		var ms int64
		var hr sql.NullInt64
		var lat, lon, ele sql.NullFloat64
		if err := rows.Scan(&x.Seq, &ms, &x.ElapsedS, &x.DistanceM, &x.SpeedKmh, &x.PowerW,
			&x.CadenceRpm, &x.GradePct, &x.Level, &hr, &lat, &lon, &ele); err != nil {
			return nil, fmt.Errorf("scan sample: %w", err)
		}
		x.At = time.UnixMilli(ms).UTC()
		x.HRBpm = int(hr.Int64)
		x.Lat, x.Lon, x.EleM = nanIfNull(lat), nanIfNull(lon), nanIfNull(ele)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	return out, nil
}

func nanIfNull(v sql.NullFloat64) float64 {
	if !v.Valid {
		return math.NaN()
	}
	return v.Float64
}

// FinishRide closes the ride with its summary and sample count.
func (s *Store) FinishRide(ctx context.Context, id int64, endedAt time.Time, sum Summary, n int) (Ride, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE rides SET ended_at=?, duration_s=?, elapsed_s=?, distance_m=?, ascent_m=?, avg_power_w=?,
			max_power_w=?, np_w=?, kj=?, avg_cadence_rpm=?, avg_speed_kmh=?, max_speed_kmh=?,
			avg_hr_bpm=?, max_hr_bpm=?, sample_count=? WHERE id=?`,
		fmtTime(endedAt), sum.DurationS, sum.ElapsedS, sum.DistanceM, sum.AscentM, sum.AvgPowerW,
		sum.MaxPowerW, sum.NPW, sum.KJ, sum.AvgCadenceRpm, sum.AvgSpeedKmh, sum.MaxSpeedKmh,
		sum.AvgHRBpm, sum.MaxHRBpm, n, id)
	if err != nil {
		return Ride{}, fmt.Errorf("finish ride: %w", err)
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return Ride{}, ErrNotFound
	}
	return s.Ride(ctx, id)
}

// Ride returns the ride by id, ErrNotFound if absent.
func (s *Store) Ride(ctx context.Context, id int64) (Ride, error) {
	r, err := scanRide(s.Read.QueryRowContext(ctx, `SELECT `+rideCols+rideFrom+`WHERE r.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ride{}, ErrNotFound
	}
	if err != nil {
		return Ride{}, fmt.Errorf("get ride: %w", err)
	}
	return r, nil
}

// ListRides returns finished rides newest first (id DESC, id < Before).
func (s *Store) ListRides(ctx context.Context, q RideQuery) ([]Ride, error) {
	where := `WHERE r.ended_at IS NOT NULL`
	var args []any
	if q.UserID != 0 {
		where += ` AND rd.user_id=?`
		args = append(args, q.UserID)
	}
	if q.RiderID != 0 {
		where += ` AND r.rider_id=?`
		args = append(args, q.RiderID)
	}
	if q.Before != 0 {
		where += ` AND r.id<?`
		args = append(args, q.Before)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	return s.queryRides(ctx, where+` ORDER BY r.id DESC LIMIT ?`, args...)
}

// OpenRides returns rides that never finished (ended_at IS NULL).
func (s *Store) OpenRides(ctx context.Context) ([]Ride, error) {
	return s.queryRides(ctx, `WHERE r.ended_at IS NULL ORDER BY r.id`)
}

// RidesUploading returns rides whose Strava status is 'uploading'.
func (s *Store) RidesUploading(ctx context.Context) ([]Ride, error) {
	return s.queryRides(ctx, `WHERE r.strava_status='uploading' ORDER BY r.id`)
}

func (s *Store) queryRides(ctx context.Context, tail string, args ...any) ([]Ride, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT `+rideCols+rideFrom+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("list rides: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ride: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rides: %w", err)
	}
	return out, nil
}

// DeleteRide removes the ride and its samples. ErrNotFound if absent.
func (s *Store) DeleteRide(ctx context.Context, id int64) error {
	res, err := s.Write.ExecContext(ctx, `DELETE FROM rides WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete ride: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimStravaUpload atomically moves a ride from none|error to uploading
// (clearing the error and upload id). It reports whether this call won.
func (s *Store) ClaimStravaUpload(ctx context.Context, id int64, now time.Time) (bool, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE rides SET strava_status='uploading', strava_error='', strava_upload_id=NULL,
			strava_updated_at=? WHERE id=? AND strava_status IN ('none','error')`, fmtTime(now), id)
	if err != nil {
		return false, fmt.Errorf("claim strava upload: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetStravaState overwrites the ride's Strava state. ErrNotFound if absent.
func (s *Store) SetStravaState(ctx context.Context, id int64, st StravaState) error {
	var upd any
	if !st.UpdatedAt.IsZero() {
		upd = fmtTime(st.UpdatedAt)
	}
	res, err := s.Write.ExecContext(ctx,
		`UPDATE rides SET strava_status=?, strava_upload_id=?, strava_activity_id=?, strava_error=?,
			strava_updated_at=? WHERE id=?`,
		st.Status, nullInt(st.UploadID), nullInt(st.ActivityID), st.Error, upd, id)
	if err != nil {
		return fmt.Errorf("set strava state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
