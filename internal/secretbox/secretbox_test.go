package secretbox_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/secretbox"
)

func newBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	for _, plain := range []string{"", "hello", "ünï©ode ✓", strings.Repeat("x", 5000)} {
		sealed := b.Seal(plain)
		if plain == "" && sealed != "" {
			t.Fatalf("empty must seal to empty, got %q", sealed)
		}
		if plain != "" && !strings.HasPrefix(sealed, "v1.") {
			t.Fatalf("missing prefix: %q", sealed)
		}
		got, err := b.Open(sealed)
		if err != nil || got != plain {
			t.Fatalf("Open = %q, %v; want %q", got, err, plain)
		}
	}
}

func TestSealRandomized(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if b.Seal("x") == b.Seal("x") {
		t.Fatal("two seals of the same plaintext are identical")
	}
}

func TestTamper(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	s := []byte(b.Seal("secret"))
	i := len(s) - 2
	if s[i] == 'A' {
		s[i] = 'B'
	} else {
		s[i] = 'A'
	}
	if _, err := b.Open(string(s)); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("tampered: err = %v, want ErrDecrypt", err)
	}
	for _, bad := range []string{"plain", "v2.abc", "v1.", "v1.!!!"} {
		if _, err := b.Open(bad); !errors.Is(err, secretbox.ErrDecrypt) {
			t.Fatalf("Open(%q) err = %v, want ErrDecrypt", bad, err)
		}
	}
}

func TestWrongKey(t *testing.T) {
	t.Parallel()
	a, b := newBox(t), newBox(t)
	if _, err := b.Open(a.Seal("secret")); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestKeyFilePerms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := secretbox.LoadOrCreateKey(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("mode %v size %d, want 0600 / 32", fi.Mode().Perm(), fi.Size())
	}
}

func TestKeyReused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := secretbox.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := secretbox.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(a.Seal("persisted"))
	if err != nil || got != "persisted" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestBadKeyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.LoadOrCreateKey(dir); !errors.Is(err, secretbox.ErrBadKey) {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
	if _, err := secretbox.NewBox(make([]byte, 5)); !errors.Is(err, secretbox.ErrBadKey) {
		t.Fatalf("NewBox err = %v, want ErrBadKey", err)
	}
}

func TestDeriveStable(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	a1, a2, c := b.Derive("a"), b.Derive("a"), b.Derive("b")
	if !bytes.Equal(a1, a2) || bytes.Equal(a1, c) || len(a1) != 32 {
		t.Fatalf("derive: equal=%v differ=%v len=%d", bytes.Equal(a1, a2), !bytes.Equal(a1, c), len(a1))
	}
}
