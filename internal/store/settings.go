package store

import (
	"context"
	"fmt"
	"time"
)

// GetSettings returns every stored settings section as section -> JSON.
func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT section, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("query settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan settings: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	return out, nil
}

// PutSettings upserts the given sections in one transaction.
func (s *Store) PutSettings(ctx context.Context, sections map[string]string) error {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(TimeFormat)
	for k, v := range sections {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings(section, value, updated_at) VALUES(?, ?, ?)
			 ON CONFLICT(section) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			k, v, now); err != nil {
			return fmt.Errorf("put settings %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}
	return nil
}
