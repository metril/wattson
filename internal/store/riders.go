package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Rider is a user's riding profile: body and bike mass for the physics
// model and the grade-to-level mapping.
type Rider struct {
	ID, UserID       int64
	Name             string
	WeightKg, BikeKg float64
	FTPW             int
	MinLevel         int
	MaxLevel         int
	Gain             float64
	OffsetLevel      float64
	DownhillScale    float64
	CdA, Crr         float64
	PowerMultiplier  float64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DefaultRider returns the profile given to a new rider.
func DefaultRider(userID int64, name string) Rider {
	return Rider{UserID: userID, Name: name, WeightKg: 75, BikeKg: 8, FTPW: 200,
		MinLevel: 1, MaxLevel: 16, Gain: 1.0, OffsetLevel: 5, DownhillScale: 0.5,
		CdA: 0.32, Crr: 0.004, PowerMultiplier: 1.0}
}

const riderCols = `id, user_id, name, weight_kg, bike_kg, ftp_w, min_level, max_level, gain,
	offset_level, downhill_scale, cda, crr, power_multiplier, created_at, updated_at`

func scanRider(r scanner) (Rider, error) {
	var x Rider
	var created, updated string
	if err := r.Scan(&x.ID, &x.UserID, &x.Name, &x.WeightKg, &x.BikeKg, &x.FTPW, &x.MinLevel,
		&x.MaxLevel, &x.Gain, &x.OffsetLevel, &x.DownhillScale, &x.CdA, &x.Crr,
		&x.PowerMultiplier, &created, &updated); err != nil {
		return Rider{}, err
	}
	var err error
	if x.CreatedAt, err = time.Parse(TimeFormat, created); err != nil {
		return Rider{}, fmt.Errorf("parse created_at: %w", err)
	}
	if x.UpdatedAt, err = time.Parse(TimeFormat, updated); err != nil {
		return Rider{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return x, nil
}

// CreateRider inserts r (ID and timestamps are assigned) and returns it.
func (s *Store) CreateRider(ctx context.Context, r Rider) (Rider, error) {
	now := time.Now().UTC()
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO riders(user_id, name, weight_kg, bike_kg, ftp_w, min_level, max_level, gain,
			offset_level, downhill_scale, cda, crr, power_multiplier, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.UserID, r.Name, r.WeightKg, r.BikeKg, r.FTPW, r.MinLevel, r.MaxLevel, r.Gain,
		r.OffsetLevel, r.DownhillScale, r.CdA, r.Crr, r.PowerMultiplier, fmtTime(now), fmtTime(now))
	if err != nil {
		return Rider{}, fmt.Errorf("create rider: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Rider{}, fmt.Errorf("create rider: %w", err)
	}
	r.ID, r.CreatedAt, r.UpdatedAt = id, now, now
	return r, nil
}

// EnsureRider inserts r only when its user has no rider yet, atomically, so
// concurrent first requests cannot create two defaults.
func (s *Store) EnsureRider(ctx context.Context, r Rider) error {
	now := fmtTime(time.Now().UTC())
	if _, err := s.Write.ExecContext(ctx,
		`INSERT INTO riders(user_id, name, weight_kg, bike_kg, ftp_w, min_level, max_level, gain,
			offset_level, downhill_scale, cda, crr, power_multiplier, created_at, updated_at)
		 SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?
		 WHERE NOT EXISTS (SELECT 1 FROM riders WHERE user_id = ?)`,
		r.UserID, r.Name, r.WeightKg, r.BikeKg, r.FTPW, r.MinLevel, r.MaxLevel, r.Gain,
		r.OffsetLevel, r.DownhillScale, r.CdA, r.Crr, r.PowerMultiplier, now, now, r.UserID); err != nil {
		return fmt.Errorf("ensure rider: %w", err)
	}
	return nil
}

// UpdateRider overwrites every profile field of the rider (r.ID, r.UserID).
// ErrNotFound unless both match.
func (s *Store) UpdateRider(ctx context.Context, r Rider) (Rider, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE riders SET name=?, weight_kg=?, bike_kg=?, ftp_w=?, min_level=?, max_level=?, gain=?,
			offset_level=?, downhill_scale=?, cda=?, crr=?, power_multiplier=?, updated_at=?
		 WHERE id=? AND user_id=?`,
		r.Name, r.WeightKg, r.BikeKg, r.FTPW, r.MinLevel, r.MaxLevel, r.Gain, r.OffsetLevel,
		r.DownhillScale, r.CdA, r.Crr, r.PowerMultiplier, fmtTime(time.Now().UTC()), r.ID, r.UserID)
	if err != nil {
		return Rider{}, fmt.Errorf("update rider: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Rider{}, ErrNotFound
	}
	return s.Rider(ctx, r.UserID, r.ID)
}

// DeleteRider removes the user's rider. ErrNotFound if it is not theirs,
// ErrLastRider if it is their only one.
func (s *Store) DeleteRider(ctx context.Context, userID, id int64) error {
	res, err := s.Write.ExecContext(ctx,
		`DELETE FROM riders WHERE id=? AND user_id=?
		 AND (SELECT COUNT(*) FROM riders WHERE user_id=?) > 1`, id, userID, userID)
	if err != nil {
		return fmt.Errorf("delete rider: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.Rider(ctx, userID, id); err != nil {
		return err // ErrNotFound or a read failure
	}
	return ErrLastRider
}

// Rider returns the user's rider by id, ErrNotFound if it is not theirs.
func (s *Store) Rider(ctx context.Context, userID, id int64) (Rider, error) {
	r, err := scanRider(s.Read.QueryRowContext(ctx,
		`SELECT `+riderCols+` FROM riders WHERE id=? AND user_id=?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Rider{}, ErrNotFound
	}
	if err != nil {
		return Rider{}, fmt.Errorf("get rider: %w", err)
	}
	return r, nil
}

// ListRiders returns the user's riders ordered by id.
func (s *Store) ListRiders(ctx context.Context, userID int64) ([]Rider, error) {
	return s.listRiders(ctx, `WHERE user_id=?`, userID)
}

// ListAllRiders returns every user's riders ordered by id.
func (s *Store) ListAllRiders(ctx context.Context) ([]Rider, error) {
	return s.listRiders(ctx, ``)
}

func (s *Store) listRiders(ctx context.Context, where string, args ...any) ([]Rider, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT `+riderCols+` FROM riders `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list riders: %w", err)
	}
	defer rows.Close()
	var out []Rider
	for rows.Next() {
		r, err := scanRider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rider: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list riders: %w", err)
	}
	return out, nil
}
