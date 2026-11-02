package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Workout is a stored interval or ERG workout. Blocks is the JSON array
// validated by package workout; the store does not interpret it.
type Workout struct {
	ID        int64
	UserID    int64 // 0 once the owning user was deleted
	Name      string
	Type      string // interval or erg
	Blocks    json.RawMessage
	DurationS int
	CreatedAt time.Time
	UpdatedAt time.Time
}

const workoutCols = `id, user_id, name, type, blocks_json, duration_s, created_at, updated_at`

func scanWorkout(r scanner) (Workout, error) {
	var w Workout
	var uid sql.NullInt64
	var blocks, created, updated string
	if err := r.Scan(&w.ID, &uid, &w.Name, &w.Type, &blocks, &w.DurationS, &created, &updated); err != nil {
		return Workout{}, err
	}
	w.UserID, w.Blocks = uid.Int64, json.RawMessage(blocks)
	var err error
	if w.CreatedAt, err = time.Parse(TimeFormat, created); err != nil {
		return Workout{}, fmt.Errorf("parse created_at: %w", err)
	}
	if w.UpdatedAt, err = time.Parse(TimeFormat, updated); err != nil {
		return Workout{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return w, nil
}

// CreateWorkout inserts w (ID and timestamps are assigned) and returns it.
// w.UserID 0 stores no owner.
func (s *Store) CreateWorkout(ctx context.Context, w Workout) (Workout, error) {
	now := time.Now().UTC()
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO workouts(user_id, name, type, blocks_json, duration_s, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		nullInt(w.UserID), w.Name, w.Type, string(w.Blocks), w.DurationS, fmtTime(now), fmtTime(now))
	if err != nil {
		return Workout{}, fmt.Errorf("create workout: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Workout{}, fmt.Errorf("create workout: %w", err)
	}
	w.ID, w.CreatedAt, w.UpdatedAt = id, now, now
	return w, nil
}

// Workout returns the workout, ErrNotFound if it does not exist.
func (s *Store) Workout(ctx context.Context, id int64) (Workout, error) {
	w, err := scanWorkout(s.Read.QueryRowContext(ctx, `SELECT `+workoutCols+` FROM workouts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Workout{}, ErrNotFound
	}
	if err != nil {
		return Workout{}, fmt.Errorf("get workout: %w", err)
	}
	return w, nil
}

// ListWorkouts returns every workout, newest first, blocks included.
func (s *Store) ListWorkouts(ctx context.Context) ([]Workout, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT `+workoutCols+` FROM workouts ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list workouts: %w", err)
	}
	defer rows.Close()
	var out []Workout
	for rows.Next() {
		w, err := scanWorkout(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workout: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workouts: %w", err)
	}
	return out, nil
}

// UpdateWorkout replaces name, type, blocks and duration of w.ID and
// returns the stored row, ErrNotFound if absent. The owner is unchanged.
func (s *Store) UpdateWorkout(ctx context.Context, w Workout) (Workout, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE workouts SET name=?, type=?, blocks_json=?, duration_s=?, updated_at=? WHERE id=?`,
		w.Name, w.Type, string(w.Blocks), w.DurationS, fmtTime(time.Now().UTC()), w.ID)
	if err != nil {
		return Workout{}, fmt.Errorf("update workout: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Workout{}, ErrNotFound
	}
	return s.Workout(ctx, w.ID)
}

// DeleteWorkout removes a workout, ErrNotFound if absent.
func (s *Store) DeleteWorkout(ctx context.Context, id int64) error {
	res, err := s.Write.ExecContext(ctx, `DELETE FROM workouts WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete workout: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
