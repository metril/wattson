package secretbox_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestAADBinding(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	sealed := b.SealFor("tok", "ha.token")
	if got, err := b.OpenFor(sealed, "ha.token"); err != nil || got != "tok" {
		t.Fatalf("OpenFor = %q, %v", got, err)
	}
	if _, err := b.OpenFor(sealed, "mapillary.token"); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("other aad: err = %v, want ErrDecrypt", err)
	}
	if _, err := b.Open(sealed); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("no aad: err = %v, want ErrDecrypt", err)
	}
}

func TestConcurrentCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const n = 16
	boxes := make([]*secretbox.Box, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			boxes[i], errs[i] = secretbox.LoadOrCreateKey(dir)
		}()
	}
	close(start)
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if !bytes.Equal(boxes[i].Derive("x"), boxes[0].Derive("x")) {
			t.Fatalf("goroutine %d got a different key", i)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "secret.key" {
		t.Fatalf("dir contents = %v, want only secret.key", ents)
	}
}

func TestKeyPermsTightened(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.LoadOrCreateKey(dir); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestDeriveIndependentOfSealingKey(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if bytes.Equal(b.Derive("wattson secretbox v1"), b.Derive("other")) {
		t.Fatal("labels collide")
	}
}
