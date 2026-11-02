// Package store owns the SQLite database: connection pools, PRAGMAs and
// schema migrations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

// Store holds two pools over the same database file. Read is sized to
// max(4, NumCPU) (WAL allows concurrent readers); Write is capped at one
// connection so writers never collide with SQLITE_BUSY.
type Store struct {
	Read  *sql.DB
	Write *sql.DB
	Path  string
}

// commonPragmas apply to every connection in both pools.
var commonPragmas = url.Values{
	"_pragma": []string{
		"journal_mode(WAL)",
		"foreign_keys(1)",
		"busy_timeout(5000)",
		"synchronous(1)",
	},
}

// Open opens (creating parent directories and the file as needed) the
// database at path, applies pending migrations and returns the Store.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}
	base := "file:" + url.PathEscape(path)

	// _txlock=immediate makes every BEGIN take the write lock up front.
	// That belongs only on the write pool: a read pool with the same
	// setting would have its BeginTx calls block on (or steal) the
	// write lock, defeating WAL's concurrent readers.
	writeQuery := url.Values{}
	for k, v := range commonPragmas {
		writeQuery[k] = v
	}
	writeQuery.Set("_txlock", "immediate")
	dsnWrite := base + "?" + writeQuery.Encode()
	dsnRead := base + "?" + commonPragmas.Encode()

	write, err := sql.Open("sqlite", dsnWrite)
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)

	read, err := sql.Open("sqlite", dsnRead)
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	readConns := max(4, runtime.NumCPU())
	read.SetMaxOpenConns(readConns)
	read.SetMaxIdleConns(readConns)

	s := &Store{Read: read, Write: write, Path: path}
	if err := migrate(context.Background(), write); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Ping verifies both pools can reach the database.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.Write.PingContext(ctx); err != nil {
		return fmt.Errorf("write pool: %w", err)
	}
	if err := s.Read.PingContext(ctx); err != nil {
		return fmt.Errorf("read pool: %w", err)
	}
	return nil
}

// Close closes both pools.
func (s *Store) Close() error {
	return errors.Join(s.Read.Close(), s.Write.Close())
}

// TimeFormat is the layout of every time column: UTC text, fixed-width so
// lexical order equals chronological order.
const TimeFormat = "2006-01-02T15:04:05.000000000Z"
