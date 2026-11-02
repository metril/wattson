package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PowerModel returns the learned power model state stored for a bike key,
// ErrNotFound if none was saved.
func (s *Store) PowerModel(ctx context.Context, key string) ([]byte, error) {
	var state string
	err := s.Read.QueryRowContext(ctx, `SELECT state FROM power_models WHERE bike_key=?`, key).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get power model: %w", err)
	}
	return []byte(state), nil
}

// PutPowerModel stores state for a bike key, replacing any earlier value.
func (s *Store) PutPowerModel(ctx context.Context, key string, state []byte) error {
	if _, err := s.Write.ExecContext(ctx,
		`INSERT INTO power_models(bike_key, state, updated_at) VALUES(?,?,?)
		 ON CONFLICT(bike_key) DO UPDATE SET state=excluded.state, updated_at=excluded.updated_at`,
		key, string(state), fmtTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("put power model: %w", err)
	}
	return nil
}

// DeletePowerModel removes the model for a bike key; absent is not an error.
func (s *Store) DeletePowerModel(ctx context.Context, key string) error {
	if _, err := s.Write.ExecContext(ctx, `DELETE FROM power_models WHERE bike_key=?`, key); err != nil {
		return fmt.Errorf("delete power model: %w", err)
	}
	return nil
}
