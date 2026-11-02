// Package secretbox seals short secrets at rest with AES-256-GCM under a
// key file kept in the data directory.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	keyFile = "secret.key"
	keyLen  = 32
	prefix  = "v1."
	// aeadLabel is the HKDF info for the sealing key, so the master key is
	// never used directly and other Derive labels stay independent of it.
	aeadLabel = "wattson secretbox v1"
	// deriveDomain prefixes every Derive label, so no Derive output can equal
	// the sealing key (whose info string lacks this prefix).
	deriveDomain = "wattson derive:"
)

var (
	// ErrDecrypt reports a sealed value that is malformed, tampered with or
	// sealed under a different key.
	ErrDecrypt = errors.New("secretbox: cannot decrypt")
	// ErrBadKey reports a key that is not exactly 32 bytes.
	ErrBadKey = errors.New("secretbox: key must be 32 bytes")
)

// Box seals and opens strings under one key.
type Box struct {
	key  []byte
	aead cipher.AEAD
}

// NewBox builds a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keyLen {
		return nil, ErrBadKey
	}
	aeadKey, err := hkdf.Key(sha256.New, key, nil, aeadLabel, keyLen)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(aeadKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{key: append([]byte(nil), key...), aead: aead}, nil
}

// LoadOrCreateKey opens dir/secret.key, creating it (mode 0600, 32 random
// bytes) when absent. Creation is atomic and durable: the key is written
// and fsynced to a temp file, then hard-linked into place (which fails if
// the file exists), so concurrent starters agree on one key and a crash
// never leaves a short key file. A pre-existing key with group/other
// permissions is chmodded to 0600 with a logged warning.
func LoadOrCreateKey(dir string) (*Box, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("secretbox: create dir: %w", err)
	}
	path := filepath.Join(dir, keyFile)
	if b, err := loadKey(path); !errors.Is(err, os.ErrNotExist) {
		return b, err
	}

	key := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("secretbox: generate key: %w", err)
	}
	tmp, err := os.CreateTemp(dir, keyFile+".tmp-*") // mode 0600
	if err != nil {
		return nil, fmt.Errorf("secretbox: create temp key: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(key)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, fmt.Errorf("secretbox: write key: %w", werr)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadKey(path) // lost the race: use the winner's key
		}
		return nil, fmt.Errorf("secretbox: install key: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync() // best effort: make the new directory entry durable
		d.Close()
	}
	return NewBox(key)
}

func loadKey(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("secretbox: read key: %w", err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			// Not fatal: e.g. a read-only or foreign-owned mount. Keep running.
			slog.Warn("secret key file is readable by others and permissions could not be tightened",
				"path", path, "mode", fi.Mode().Perm().String(), "err", err)
		} else {
			slog.Warn("secret key file was readable by others; permissions reset to 0600",
				"path", path, "was", fi.Mode().Perm().String())
		}
	}
	return NewBox(key)
}

// SealFor returns "v1." + base64url(nonce || ciphertext); "" seals to "".
// aad (for example "ha.token") is authenticated but not stored: the value
// only opens with the same aad, so a sealed value cannot be moved to
// another field.
func (b *Box) SealFor(plain, aad string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic("secretbox: entropy source failed: " + err.Error())
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), []byte(aad))
	return prefix + base64.RawURLEncoding.EncodeToString(out)
}

// OpenFor reverses SealFor; "" opens to "". Anything that is not a valid
// sealed value under this key and aad yields ErrDecrypt.
func (b *Box) OpenFor(sealed, aad string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	enc, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return "", ErrDecrypt
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	ns := b.aead.NonceSize()
	if err != nil || len(raw) < ns+b.aead.Overhead() {
		return "", ErrDecrypt
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], []byte(aad))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// Derive returns a 32-byte subkey for label (HKDF-SHA256, no salt; info is
// "wattson derive:" + label, disjoint from the sealing key's info).
func (b *Box) Derive(label string) []byte {
	k, err := hkdf.Key(sha256.New, b.key, nil, deriveDomain+label, keyLen)
	if err != nil {
		panic("secretbox: hkdf: " + err.Error())
	}
	return k
}
