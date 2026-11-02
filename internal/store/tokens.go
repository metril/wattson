package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// APIToken is token metadata; the secret itself is only ever stored hashed.
type APIToken struct {
	ID, UserID int64
	Name       string
	Prefix     string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

const tokenCols = `id, user_id, name, prefix, scopes, created_at, last_used_at`

func scanToken(r scanner) (APIToken, error) {
	var t APIToken
	var scopes, created string
	var used sql.NullString
	if err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes, &created, &used); err != nil {
		return APIToken{}, err
	}
	return t, t.parse(scopes, created, used)
}

// parse decodes the text columns of a token row into t.
func (t *APIToken) parse(scopes, created string, used sql.NullString) error {
	if err := json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return fmt.Errorf("parse scopes: %w", err)
	}
	var err error
	if t.CreatedAt, err = time.Parse(TimeFormat, created); err != nil {
		return fmt.Errorf("parse created_at: %w", err)
	}
	if used.Valid {
		u, err := time.Parse(TimeFormat, used.String)
		if err != nil {
			return fmt.Errorf("parse last_used_at: %w", err)
		}
		t.LastUsedAt = &u
	}
	return nil
}

// CreateAPIToken stores a token by the hash of its secret. ErrNameConflict
// if the hash already exists.
func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name, prefix, hash string, scopes []string) (APIToken, error) {
	if scopes == nil {
		scopes = []string{}
	}
	js, err := json.Marshal(scopes)
	if err != nil {
		return APIToken{}, fmt.Errorf("encode scopes: %w", err)
	}
	now := time.Now().UTC()
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO api_tokens(user_id, name, prefix, hash, scopes, created_at) VALUES(?,?,?,?,?,?)`,
		userID, name, prefix, hash, string(js), fmtTime(now))
	if err != nil {
		if isUnique(err) {
			return APIToken{}, ErrNameConflict
		}
		return APIToken{}, fmt.Errorf("create api token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIToken{}, fmt.Errorf("create api token: %w", err)
	}
	return APIToken{ID: id, UserID: userID, Name: name, Prefix: prefix, Scopes: scopes, CreatedAt: now}, nil
}

// ListAPITokens returns the user's tokens ordered by id.
func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.Read.QueryContext(ctx,
		`SELECT `+tokenCols+` FROM api_tokens WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	return out, nil
}

// DeleteAPIToken removes a token owned by userID; ErrNotFound otherwise.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id int64) error {
	if err := s.execOne(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	return nil
}

// TokenUser resolves a token hash to the token and its owner. ErrNotFound if
// the hash is unknown or the owner is disabled.
func (s *Store) TokenUser(ctx context.Context, hash string) (APIToken, User, error) {
	row := s.Read.QueryRowContext(ctx,
		`SELECT t.id, t.user_id, t.name, t.prefix, t.scopes, t.created_at, t.last_used_at,
		        `+userColsU+`
		 FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.hash = ? AND u.disabled = 0`, hash)
	var t APIToken
	var scopes, created string
	var used sql.NullString
	var u User
	var ucreated string
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes, &created, &used,
		&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.PasswordHash,
		&u.Iss, &u.Sub, &u.IsAdmin, &u.IsBootstrap, &u.Disabled, &ucreated)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, User{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, User{}, fmt.Errorf("token user: %w", err)
	}
	if err := t.parse(scopes, created, used); err != nil {
		return APIToken{}, User{}, err
	}
	if u.CreatedAt, err = time.Parse(TimeFormat, ucreated); err != nil {
		return APIToken{}, User{}, fmt.Errorf("parse created_at: %w", err)
	}
	return t, u, nil
}

// TouchAPIToken records last use; ErrNotFound if the token no longer exists.
func (s *Store) TouchAPIToken(ctx context.Context, id int64, at time.Time) error {
	if err := s.execOne(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, fmtTime(at), id); err != nil {
		return fmt.Errorf("touch api token: %w", err)
	}
	return nil
}
