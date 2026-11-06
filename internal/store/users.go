package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User is an account row. Empty Email, PasswordHash, Iss and Sub are stored
// as NULL.
type User struct {
	ID           int64
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string
	Iss, Sub     string
	IsAdmin      bool
	IsBootstrap  bool
	Disabled     bool
	Units        string // "metric" or "imperial"
	CreatedAt    time.Time
}

// ErrInvalidUnits is returned for a units value other than metric or imperial.
var ErrInvalidUnits = errors.New("store: units must be metric or imperial")

var userCols = userColumns("")

// userColumns returns the users column list qualified by alias (empty for
// none), for joins that select users columns.
func userColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id, " + alias + "username, COALESCE(" + alias + "email,''), " + alias + "display_name, " +
		"COALESCE(" + alias + "password_hash,''), COALESCE(" + alias + "iss,''), COALESCE(" + alias + "sub,''), " +
		alias + "is_admin, " + alias + "is_bootstrap, " + alias + "disabled, " + alias + "units, " + alias + "created_at"
}

type scanner interface{ Scan(dest ...any) error }

func scanUser(r scanner) (User, error) {
	var u User
	var created string
	if err := r.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.PasswordHash,
		&u.Iss, &u.Sub, &u.IsAdmin, &u.IsBootstrap, &u.Disabled, &u.Units, &created); err != nil {
		return User{}, err
	}
	t, err := time.Parse(TimeFormat, created)
	if err != nil {
		return User{}, fmt.Errorf("parse created_at: %w", err)
	}
	u.CreatedAt = t
	return u, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fmtTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// isUnique reports whether err is a SQLite UNIQUE / PRIMARY KEY violation.
func isUnique(err error) bool {
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		switch coder.Code() {
		case 1555, 2067: // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
			return true
		}
	}
	return false
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func insertUser(ctx context.Context, x execer, u User) (User, error) {
	if (u.Iss == "") != (u.Sub == "") {
		return User{}, ErrIssSubMismatch
	}
	now := time.Now().UTC()
	res, err := x.ExecContext(ctx,
		`INSERT INTO users(username, email, display_name, password_hash, iss, sub,
		                   is_admin, is_bootstrap, disabled, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		u.Username, nullStr(u.Email), u.DisplayName, nullStr(u.PasswordHash),
		nullStr(u.Iss), nullStr(u.Sub), u.IsAdmin, u.IsBootstrap, u.Disabled, fmtTime(now))
	if err != nil {
		if isUnique(err) {
			return User{}, ErrNameConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	u.ID, u.CreatedAt = id, now
	return u, nil
}

// CreateBootstrapAdmin creates the first-run admin. The emptiness check and
// the insert share one write transaction, so exactly one concurrent caller
// succeeds; the rest get ErrAlreadySetup.
func (s *Store) CreateBootstrapAdmin(ctx context.Context, username, displayName, pwHash string) (User, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if n > 0 {
		return User{}, ErrAlreadySetup
	}
	u, err := insertUser(ctx, tx, User{
		Username: username, DisplayName: displayName, PasswordHash: pwHash,
		IsAdmin: true, IsBootstrap: true,
	})
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// CreateUser inserts u (ID and CreatedAt are assigned). ErrNameConflict on a
// duplicate username (case-insensitive) or (iss, sub), ErrIssSubMismatch if
// exactly one of Iss and Sub is set.
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	return insertUser(ctx, s.Write, u)
}

func (s *Store) oneUser(ctx context.Context, where string, args ...any) (User, error) {
	u, err := scanUser(s.Read.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}

// UserByID returns the user with id, or ErrNotFound.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.oneUser(ctx, `id = ?`, id)
}

// UserByUsername matches case-insensitively; ErrNotFound if absent.
func (s *Store) UserByUsername(ctx context.Context, name string) (User, error) {
	return s.oneUser(ctx, `username = ?`, name)
}

// UserByIssSub returns the OIDC-linked user; ErrNotFound if absent.
func (s *Store) UserByIssSub(ctx context.Context, iss, sub string) (User, error) {
	if iss == "" {
		return User{}, ErrNotFound
	}
	return s.oneUser(ctx, `iss = ? AND sub = ?`, iss, sub)
}

// UserByEmail finds a local (iss IS NULL) user by email, matching ASCII
// case-insensitively. users.email has no unique index, so if more than one
// local user has the address it returns ErrAmbiguous rather than picking one;
// callers (such as OIDC link-by-email) must refuse to link in that case.
// ErrNotFound if none match or email is empty.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	if email == "" {
		return User{}, ErrNotFound
	}
	rows, err := s.Read.QueryContext(ctx,
		`SELECT `+userCols+` FROM users WHERE iss IS NULL AND email = ? COLLATE NOCASE ORDER BY id LIMIT 2`, email)
	if err != nil {
		return User{}, fmt.Errorf("query user by email: %w", err)
	}
	defer rows.Close()
	var found []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return User{}, fmt.Errorf("scan user: %w", err)
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		return User{}, fmt.Errorf("read users: %w", err)
	}
	switch len(found) {
	case 0:
		return User{}, ErrNotFound
	case 1:
		return found[0], nil
	}
	return User{}, ErrAmbiguous
}

// ListUsers returns every user ordered by id.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.Read.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.Write.ExecContext(ctx, q, args...)
	if err != nil {
		if isUnique(err) {
			return ErrNameConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPassword replaces the user's password hash; ErrNotFound if no such user.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string) error {
	if err := s.execOne(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, nullStr(hash), id); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// UpdateDisplayName sets the user's display name and, in the same transaction,
// renames the user's riders whose name equals the previous display name (or the
// username when none was set); manually renamed riders keep their names.
// ErrNotFound if no such user.
func (s *Store) UpdateDisplayName(ctx context.Context, id int64, name string) (User, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	old := u.DisplayName
	if old == "" {
		old = u.Username
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name = ? WHERE id = ?`, name, id); err != nil {
		return User{}, fmt.Errorf("update display name: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE riders SET name = ?, updated_at = ? WHERE user_id = ? AND name = ?`, name, fmtTime(time.Now().UTC()), id, old); err != nil {
		return User{}, fmt.Errorf("rename riders: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	u.DisplayName = name
	return u, nil
}

// UpdateUnits sets the user's unit preference ("metric" or "imperial");
// ErrInvalidUnits for other values, ErrNotFound if no such user.
func (s *Store) UpdateUnits(ctx context.Context, id int64, units string) error {
	if units != "metric" && units != "imperial" {
		return ErrInvalidUnits
	}
	if err := s.execOne(ctx, `UPDATE users SET units = ? WHERE id = ?`, units, id); err != nil {
		return fmt.Errorf("update units: %w", err)
	}
	return nil
}

// LinkOIDC attaches an OIDC identity to the user. ErrNotFound if no such
// user, ErrNameConflict if the identity is already linked to someone and
// ErrIssSubMismatch unless iss and sub are both set or both empty (unlink).
func (s *Store) LinkOIDC(ctx context.Context, id int64, iss, sub string) error {
	if (iss == "") != (sub == "") {
		return ErrIssSubMismatch
	}
	if err := s.execOne(ctx, `UPDATE users SET iss = ?, sub = ? WHERE id = ?`, nullStr(iss), nullStr(sub), id); err != nil {
		return fmt.Errorf("link oidc: %w", err)
	}
	return nil
}

// UpdateUserFlags sets IsAdmin and/or Disabled (nil leaves a flag alone). It
// returns ErrBootstrap if the change would demote or disable the bootstrap
// user and ErrLastAdmin if it would leave no enabled admin. The checks and
// the write share one write transaction.
func (s *Store) UpdateUserFlags(ctx context.Context, id int64, isAdmin, disabled *bool) (User, error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	newAdmin, newDisabled := u.IsAdmin, u.Disabled
	if isAdmin != nil {
		newAdmin = *isAdmin
	}
	if disabled != nil {
		newDisabled = *disabled
	}
	if u.IsBootstrap && (!newAdmin || newDisabled) {
		return User{}, ErrBootstrap
	}
	if u.IsAdmin && !u.Disabled && (!newAdmin || newDisabled) {
		var others int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users WHERE is_admin = 1 AND disabled = 0 AND id <> ?`, id).Scan(&others); err != nil {
			return User{}, fmt.Errorf("count admins: %w", err)
		}
		if others == 0 {
			return User{}, ErrLastAdmin
		}
	}
	if newAdmin != u.IsAdmin || newDisabled != u.Disabled {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = ?, disabled = ? WHERE id = ?`,
			newAdmin, newDisabled, id); err != nil {
			return User{}, fmt.Errorf("update flags: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	u.IsAdmin, u.Disabled = newAdmin, newDisabled
	return u, nil
}

// EmailInUse reports whether any user, local or OIDC-linked, has email
// (ASCII case-insensitive). An empty email is never in use.
func (s *Store) EmailInUse(ctx context.Context, email string) (bool, error) {
	if email == "" {
		return false, nil
	}
	var n int
	if err := s.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE email = ? COLLATE NOCASE`, email).Scan(&n); err != nil {
		return false, fmt.Errorf("check email: %w", err)
	}
	return n > 0, nil
}
