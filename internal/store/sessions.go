package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateSession stores a session keyed by the hash of its cookie value.
func (s *Store) CreateSession(ctx context.Context, idHash string, userID int64, created, expires time.Time) error {
	if _, err := s.Write.ExecContext(ctx,
		`INSERT INTO sessions(id_hash, user_id, created_at, expires_at) VALUES(?,?,?,?)`,
		idHash, userID, fmtTime(created), fmtTime(expires)); err != nil {
		if isUnique(err) {
			return ErrNameConflict
		}
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionUser returns the owner of a live session. ErrNotFound if the
// session is missing or expired (now >= expires) or the user is disabled.
func (s *Store) SessionUser(ctx context.Context, idHash string, now time.Time) (User, error) {
	u, err := scanUser(s.Read.QueryRowContext(ctx,
		`SELECT `+userColsU+` FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id_hash = ? AND s.expires_at > ? AND u.disabled = 0`, idHash, fmtTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("session user: %w", err)
	}
	return u, nil
}

// DeleteSession removes one session; a missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	if _, err := s.Write.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions removes every session of a user and returns how many.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) (int64, error) {
	return s.deleteCount(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
}

// DeleteExpiredSessions removes sessions with expires_at <= now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	return s.deleteCount(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, fmtTime(now))
}

func (s *Store) deleteCount(ctx context.Context, q string, args ...any) (int64, error) {
	res, err := s.Write.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("delete sessions: %w", err)
	}
	return res.RowsAffected()
}
