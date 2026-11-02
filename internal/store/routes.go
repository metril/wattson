package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Route is a stored route: summary statistics, the simplified GeoJSON line
// and the encoded elevation profile (route.Encode).
type Route struct {
	ID          int64
	UserID      int64 // 0 once the owning user was deleted
	Name        string
	Source      string // gpx, tcx, fit, draw or synthetic
	DistanceM   float64
	AscentM     float64
	DescentM    float64
	MinGradePct float64
	MaxGradePct float64
	BBox        [4]float64 // W S E N, degrees
	GeoJSON     json.RawMessage
	Spec        json.RawMessage
	Points      []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const routeSummaryCols = `id, user_id, name, source, distance_m, ascent_m, descent_m, min_grade_pct,
	max_grade_pct, bbox, created_at, updated_at`

func scanRoute(r scanner, full bool) (Route, error) {
	var x Route
	var uid sql.NullInt64
	var bbox, created, updated string
	dst := []any{&x.ID, &uid, &x.Name, &x.Source, &x.DistanceM, &x.AscentM, &x.DescentM,
		&x.MinGradePct, &x.MaxGradePct, &bbox, &created, &updated}
	var geo, spec string
	if full {
		dst = append(dst, &geo, &spec, &x.Points)
	}
	if err := r.Scan(dst...); err != nil {
		return Route{}, err
	}
	x.UserID = uid.Int64
	if err := json.Unmarshal([]byte(bbox), &x.BBox); err != nil {
		return Route{}, fmt.Errorf("parse bbox: %w", err)
	}
	var err error
	if x.CreatedAt, err = time.Parse(TimeFormat, created); err != nil {
		return Route{}, fmt.Errorf("parse created_at: %w", err)
	}
	if x.UpdatedAt, err = time.Parse(TimeFormat, updated); err != nil {
		return Route{}, fmt.Errorf("parse updated_at: %w", err)
	}
	if full {
		x.GeoJSON, x.Spec = json.RawMessage(geo), json.RawMessage(spec)
	}
	return x, nil
}

// CreateRoute inserts r (ID and timestamps are assigned) and returns it.
// r.UserID 0 stores no owner.
func (s *Store) CreateRoute(ctx context.Context, r Route) (Route, error) {
	bbox, err := json.Marshal(r.BBox)
	if err != nil {
		return Route{}, fmt.Errorf("create route: %w", err)
	}
	if len(r.Spec) == 0 {
		r.Spec = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO routes(user_id, name, source, distance_m, ascent_m, descent_m, min_grade_pct,
			max_grade_pct, bbox, geojson, spec, points, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nullInt(r.UserID), r.Name, r.Source, r.DistanceM, r.AscentM, r.DescentM, r.MinGradePct,
		r.MaxGradePct, string(bbox), string(r.GeoJSON), string(r.Spec), r.Points, fmtTime(now), fmtTime(now))
	if err != nil {
		return Route{}, fmt.Errorf("create route: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Route{}, fmt.Errorf("create route: %w", err)
	}
	r.ID, r.CreatedAt, r.UpdatedAt = id, now, now
	return r, nil
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// Route returns the full row, ErrNotFound if it does not exist.
func (s *Store) Route(ctx context.Context, id int64) (Route, error) {
	r, err := scanRoute(s.Read.QueryRowContext(ctx,
		`SELECT `+routeSummaryCols+`, geojson, spec, points FROM routes WHERE id=?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return Route{}, ErrNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("get route: %w", err)
	}
	return r, nil
}

// ListRoutes returns every route, newest first, without GeoJSON, Spec and Points.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.Read.QueryContext(ctx,
		`SELECT `+routeSummaryCols+` FROM routes ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		r, err := scanRoute(rows, false)
		if err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	return out, nil
}

// RenameRoute sets the name and returns the full row, ErrNotFound if absent.
func (s *Store) RenameRoute(ctx context.Context, id int64, name string) (Route, error) {
	res, err := s.Write.ExecContext(ctx, `UPDATE routes SET name=?, updated_at=? WHERE id=?`,
		name, fmtTime(time.Now().UTC()), id)
	if err != nil {
		return Route{}, fmt.Errorf("rename route: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Route{}, ErrNotFound
	}
	return s.Route(ctx, id)
}

// DeleteRoute removes a route, ErrNotFound if absent.
func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	res, err := s.Write.ExecContext(ctx, `DELETE FROM routes WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete route: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
