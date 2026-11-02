// Package storetest provides a throwaway Store for tests.
package storetest

import (
	"path/filepath"
	"testing"

	"github.com/metril/wattson/internal/store"
)

// New opens a fresh database at t.TempDir()/wattson.db, closed on Cleanup.
func New(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "wattson.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
