package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/metril/wattson/internal/stats"
)

// StatsVersion is the current revision of the per-ride metrics. Finished
// rides below it are recomputed by the startup backfill.
const StatsVersion = 1

// Summary holds the aggregate figures of a finished ride. Zero HR = none.
type Summary struct {
	DurationS, ElapsedS, DistanceM, AscentM float64
	AvgPowerW, MaxPowerW, NPW, KJ           float64
	AvgCadenceRpm, AvgSpeedKmh, MaxSpeedKmh float64
	AvgHRBpm, MaxHRBpm                      int
	MaxCadenceRpm                           float64
	// Derived metrics; nil when they cannot be computed.
	IntensityFactor, TSS, VI, EF, DecouplingPct, VAM *float64
	Completed                                        bool
	// Curve is the power curve; PowerZones and HRZones are seconds per zone
	// (nil when FTP or heart rate is unknown).
	Curve               []stats.Point
	PowerZones, HRZones []float64
	StatsVersion        int
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
	// Snapshots taken when the ride was created; zero = unknown.
	FTPW, HRMaxBpm           int
	WeightKg, RouteDistanceM float64
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
	r.strava_activity_id, r.strava_error, r.strava_updated_at, r.ftp_w, r.weight_kg, r.hr_max_bpm,
	r.route_distance_m, r.completed, r.intensity_factor, r.tss, r.variability_index,
	r.efficiency_factor, r.decoupling_pct, r.vam_mph, r.max_cadence_rpm, r.stats_version`

const rideFrom = ` FROM rides r JOIN riders rd ON rd.id = r.rider_id `

func scanRide(r scanner) (Ride, error) {
	var x Ride
	var route, upload, activity sql.NullInt64
	var started string
	var ended, supd sql.NullString
	var ftp, hrMax sql.NullInt64
	var wt, rdist, ifac, tss, vi, ef, dec, vam sql.NullFloat64
	s := &x.Summary
	if err := r.Scan(&x.ID, &x.RiderID, &x.UserID, &x.RiderName, &route, &x.RouteName, &x.Mode,
		&started, &ended, &s.DurationS, &s.ElapsedS, &s.DistanceM, &s.AscentM, &s.AvgPowerW,
		&s.MaxPowerW, &s.NPW, &s.KJ, &s.AvgCadenceRpm, &s.AvgSpeedKmh, &s.MaxSpeedKmh,
		&s.AvgHRBpm, &s.MaxHRBpm, &x.SampleCount, &x.Strava.Status, &upload, &activity,
		&x.Strava.Error, &supd, &ftp, &wt, &hrMax, &rdist, &s.Completed, &ifac, &tss, &vi, &ef,
		&dec, &vam, &s.MaxCadenceRpm, &s.StatsVersion); err != nil {
		return Ride{}, err
	}
	x.RouteID, x.Strava.UploadID, x.Strava.ActivityID = route.Int64, upload.Int64, activity.Int64
	x.FTPW, x.HRMaxBpm, x.WeightKg, x.RouteDistanceM = int(ftp.Int64), int(hrMax.Int64), wt.Float64, rdist.Float64
	s.IntensityFactor, s.TSS, s.VI, s.EF = ptrF(ifac), ptrF(tss), ptrF(vi), ptrF(ef)
	s.DecouplingPct, s.VAM = ptrF(dec), ptrF(vam)
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

func ptrF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func derefF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// CreateRide inserts an open ride (no end time) for r.RiderID, snapshotting
// the rider's FTP, weight and max HR and the route's distance. ErrNotFound if
// the rider does not exist.
func (s *Store) CreateRide(ctx context.Context, r Ride) (Ride, error) {
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO rides(rider_id, route_id, route_name, mode, started_at, ftp_w, weight_kg, hr_max_bpm,
			route_distance_m)
		 SELECT ?,?,?,?,?, rd.ftp_w, rd.weight_kg, rd.max_hr_bpm, (SELECT distance_m FROM routes WHERE id=?)
		 FROM riders rd WHERE rd.id=?`,
		r.RiderID, nullInt(r.RouteID), r.RouteName, r.Mode, fmtTime(r.StartedAt), nullInt(r.RouteID), r.RiderID)
	if err != nil {
		return Ride{}, fmt.Errorf("create ride: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Ride{}, fmt.Errorf("create ride: rider %d: %w", r.RiderID, ErrNotFound)
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

// statsSet assigns the derived-metric columns shared by FinishRide and ApplyRideStats.
const statsSet = `completed=?, intensity_factor=?, tss=?, variability_index=?, efficiency_factor=?,
	decoupling_pct=?, vam_mph=?, max_cadence_rpm=?, stats_version=?`

func statsArgs(sum Summary) []any {
	return []any{sum.Completed, derefF(sum.IntensityFactor), derefF(sum.TSS), derefF(sum.VI), derefF(sum.EF),
		derefF(sum.DecouplingPct), derefF(sum.VAM), sum.MaxCadenceRpm, sum.StatsVersion}
}

// FinishRide closes an open ride with its summary and sample count, writing
// the ride row, power curve and zones in one transaction.
// ErrNotFound if absent, ErrAlreadyFinished if it already has an end time.
func (s *Store) FinishRide(ctx context.Context, id int64, endedAt time.Time, sum Summary, n int) (Ride, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return Ride{}, fmt.Errorf("finish ride: %w", err)
	}
	defer tx.Rollback()
	args := append([]any{fmtTime(endedAt), sum.DurationS, sum.ElapsedS, sum.DistanceM, sum.AscentM, sum.AvgPowerW,
		sum.MaxPowerW, sum.NPW, sum.KJ, sum.AvgCadenceRpm, sum.AvgSpeedKmh, sum.MaxSpeedKmh,
		sum.AvgHRBpm, sum.MaxHRBpm, n}, statsArgs(sum)...)
	res, err := tx.ExecContext(ctx,
		`UPDATE rides SET ended_at=?, duration_s=?, elapsed_s=?, distance_m=?, ascent_m=?, avg_power_w=?,
			max_power_w=?, np_w=?, kj=?, avg_cadence_rpm=?, avg_speed_kmh=?, max_speed_kmh=?,
			avg_hr_bpm=?, max_hr_bpm=?, sample_count=?, `+statsSet+` WHERE id=? AND ended_at IS NULL`,
		append(args, id)...)
	if err != nil {
		return Ride{}, fmt.Errorf("finish ride: %w", err)
	}
	if c, _ := res.RowsAffected(); c == 0 {
		if _, err := s.Ride(ctx, id); err != nil {
			return Ride{}, err
		}
		return Ride{}, ErrAlreadyFinished
	}
	if err := writeDetail(ctx, tx, id, sum); err != nil {
		return Ride{}, fmt.Errorf("finish ride: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Ride{}, fmt.Errorf("finish ride: %w", err)
	}
	return s.Ride(ctx, id)
}

// Snapshot is the rider and route state a ride's metrics were computed with.
type Snapshot struct {
	FTPW, HRMaxBpm           int
	WeightKg, RouteDistanceM float64
}

// ApplyRideStats stores recomputed metrics for a finished ride, filling any
// snapshot the ride lacks, only while its stats_version is below
// sum.StatsVersion. It reports whether the ride was updated.
func (s *Store) ApplyRideStats(ctx context.Context, id int64, sum Summary, snap Snapshot) (bool, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("apply ride stats: %w", err)
	}
	defer tx.Rollback()
	args := append(statsArgs(sum), nullInt(int64(snap.FTPW)), nullInt0(snap.WeightKg), nullInt(int64(snap.HRMaxBpm)),
		nullInt0(snap.RouteDistanceM), id, sum.StatsVersion)
	res, err := tx.ExecContext(ctx,
		`UPDATE rides SET `+statsSet+`, ftp_w=COALESCE(ftp_w,?), weight_kg=COALESCE(weight_kg,?),
			hr_max_bpm=COALESCE(hr_max_bpm,?), route_distance_m=COALESCE(route_distance_m,?)
		 WHERE id=? AND ended_at IS NOT NULL AND stats_version<?`, args...)
	if err != nil {
		return false, fmt.Errorf("apply ride stats: %w", err)
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return false, nil
	}
	if err := writeDetail(ctx, tx, id, sum); err != nil {
		return false, fmt.Errorf("apply ride stats: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("apply ride stats: %w", err)
	}
	return true, nil
}

func nullInt0(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

// writeDetail replaces the ride's power curve and zones with one multi-row
// INSERT each.
func writeDetail(ctx context.Context, tx *sql.Tx, id int64, sum Summary) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM ride_power_curve WHERE ride_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ride_zones WHERE ride_id=?`, id); err != nil {
		return err
	}
	if len(sum.Curve) > 0 {
		var b strings.Builder
		b.WriteString(`INSERT INTO ride_power_curve(ride_id, dur_s, watts) VALUES `)
		args := make([]any, 0, 3*len(sum.Curve))
		for i, p := range sum.Curve {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?,?,?)")
			args = append(args, id, p.DurS, p.Watts)
		}
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	nz := len(sum.PowerZones) + len(sum.HRZones)
	if nz == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO ride_zones(ride_id, kind, zone, seconds) VALUES `)
	args := make([]any, 0, 4*nz)
	add := func(kind string, zs []float64) {
		for z, sec := range zs {
			if len(args) > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?,?,?,?)")
			args = append(args, id, kind, z+1, sec)
		}
	}
	add("power", sum.PowerZones)
	add("hr", sum.HRZones)
	_, err := tx.ExecContext(ctx, b.String(), args...)
	return err
}

// FillRideSnapshot stores the snapshot values the ride lacks, leaving set ones.
func (s *Store) FillRideSnapshot(ctx context.Context, id int64, snap Snapshot) error {
	_, err := s.Write.ExecContext(ctx,
		`UPDATE rides SET ftp_w=COALESCE(ftp_w,?), weight_kg=COALESCE(weight_kg,?),
			hr_max_bpm=COALESCE(hr_max_bpm,?), route_distance_m=COALESCE(route_distance_m,?) WHERE id=?`,
		nullInt(int64(snap.FTPW)), nullInt0(snap.WeightKg), nullInt(int64(snap.HRMaxBpm)),
		nullInt0(snap.RouteDistanceM), id)
	if err != nil {
		return fmt.Errorf("fill ride snapshot: %w", err)
	}
	return nil
}

// RidesNeedingStats returns up to limit finished rides with id above afterID
// whose stats_version is below StatsVersion, oldest first.
func (s *Store) RidesNeedingStats(ctx context.Context, afterID int64, limit int) ([]Ride, error) {
	return s.queryRides(ctx, `WHERE r.ended_at IS NOT NULL AND r.stats_version<? AND r.id>? ORDER BY r.id LIMIT ?`,
		StatsVersion, afterID, limit)
}

// RouteDistance returns the route's length in metres, ErrNotFound if absent.
func (s *Store) RouteDistance(ctx context.Context, id int64) (float64, error) {
	var d float64
	err := s.Read.QueryRowContext(ctx, `SELECT distance_m FROM routes WHERE id=?`, id).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("route distance: %w", err)
	}
	return d, nil
}

// RideExtras are a ride's peak powers and zone times, for display.
type RideExtras struct {
	PeakW               map[int]float64 // by duration in seconds: 5, 60, 300, 1200, 3600
	PowerZones, HRZones []float64
}

// PeakDurations are the power-curve durations reported on a ride.
var PeakDurations = [...]int{5, 60, 300, 1200, 3600}

// RideExtras loads the peaks and zones of the rides in two queries.
func (s *Store) RideExtras(ctx context.Context, ids []int64) (map[int64]RideExtras, error) {
	out := make(map[int64]RideExtras, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.Read.QueryContext(ctx,
		`SELECT ride_id, dur_s, watts FROM ride_power_curve WHERE ride_id IN (`+in+`)
		 AND dur_s IN (5,60,300,1200,3600)`, args...)
	if err != nil {
		return nil, fmt.Errorf("ride peaks: %w", err)
	}
	for rows.Next() {
		var id int64
		var d int
		var w float64
		if err := rows.Scan(&id, &d, &w); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan peak: %w", err)
		}
		x := out[id]
		if x.PeakW == nil {
			x.PeakW = make(map[int]float64, len(PeakDurations))
		}
		x.PeakW[d] = w
		out[id] = x
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("ride peaks: %w", err)
	}
	rows.Close()
	zr, err := s.Read.QueryContext(ctx,
		`SELECT ride_id, kind, zone, seconds FROM ride_zones WHERE ride_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("ride zones: %w", err)
	}
	defer zr.Close()
	for zr.Next() {
		var id int64
		var kind string
		var z int
		var sec float64
		if err := zr.Scan(&id, &kind, &z, &sec); err != nil {
			return nil, fmt.Errorf("scan zone: %w", err)
		}
		x := out[id]
		dst := &x.PowerZones
		if kind == "hr" {
			dst = &x.HRZones
		}
		for len(*dst) < z {
			*dst = append(*dst, 0)
		}
		(*dst)[z-1] = sec
		out[id] = x
	}
	return out, zr.Err()
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

// ClaimStravaUpload atomically moves a finished ride from none|error to uploading
// (clearing the error and upload id). It reports whether this call won.
func (s *Store) ClaimStravaUpload(ctx context.Context, id int64, now time.Time) (bool, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE rides SET strava_status='uploading', strava_error='', strava_upload_id=NULL,
			strava_updated_at=? WHERE id=? AND ended_at IS NOT NULL AND strava_status IN ('none','error')`, fmtTime(now), id)
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
