package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestOpenCreatesSchema(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	rows, err := s.Read.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	want := []string{"api_tokens", "oidc_providers", "riders", "routes", "schema_migrations", "sessions", "settings", "users"}
	if !slices.Equal(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func TestOpenIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "w.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.Read.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("migrations = %d, want 2", n)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPragmas(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	for name, pool := range map[string]*sql.DB{"read": s.Read, "write": s.Write} {
		var jm string
		if err := pool.QueryRow(`PRAGMA journal_mode`).Scan(&jm); err != nil {
			t.Fatal(err)
		}
		if jm != "wal" {
			t.Errorf("%s journal_mode = %q", name, jm)
		}
		var fk int
		if err := pool.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Errorf("%s foreign_keys = %d", name, fk)
		}
	}
}

func TestUserDeleteCascades(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	const ts = "2026-01-01T00:00:00.000000000Z"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Write.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users(id, username, created_at) VALUES(1, 'a', ?)`, ts)
	exec(`INSERT INTO sessions(id_hash, user_id, created_at, expires_at) VALUES('h', 1, ?, ?)`, ts, ts)
	exec(`INSERT INTO api_tokens(user_id, name, prefix, hash, scopes, created_at) VALUES(1, 'n', 'p', 'hh', '[]', ?)`, ts)
	exec(`INSERT INTO riders(user_id, name, weight_kg, bike_kg, ftp_w, min_level, max_level, gain, offset_level, downhill_scale, cda, crr, power_multiplier, created_at, updated_at)
		VALUES(1, 'r', 75, 10, 200, 1, 32, 1, 0, 1, 0.3, 0.005, 1, ?, ?)`, ts, ts)
	exec(`DELETE FROM users WHERE id = 1`)
	for _, tbl := range []string{"sessions", "api_tokens", "riders"} {
		var n int
		if err := s.Read.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s rows = %d, want 0", tbl, n)
		}
	}
}
