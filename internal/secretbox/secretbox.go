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
	"os"
	"path/filepath"
	"strings"
)

const (
	keyFile = "secret.key"
	keyLen  = 32
	prefix  = "v1."
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
	blk, err := aes.NewCipher(key)
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
// bytes) when absent. O_EXCL keeps concurrent starters from overwriting
// each other's key.
func LoadOrCreateKey(dir string) (*Box, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("secretbox: create dir: %w", err)
	}
	path := filepath.Join(dir, keyFile)
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("secretbox: generate key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case err == nil:
		_, werr := f.Write(key)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(path)
			return nil, fmt.Errorf("secretbox: write key: %w", werr)
		}
		return NewBox(key)
	case errors.Is(err, os.ErrExist):
		existing, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("secretbox: read key: %w", rerr)
		}
		return NewBox(existing)
	default:
		return nil, fmt.Errorf("secretbox: create key: %w", err)
	}
}

// Seal returns "v1." + base64url(nonce || ciphertext); "" seals to "".
func (b *Box) Seal(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic("secretbox: entropy source failed: " + err.Error())
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawURLEncoding.EncodeToString(out)
}

// Open reverses Seal; "" opens to "". Anything else that is not a valid
// sealed value under this key yields ErrDecrypt.
func (b *Box) Open(sealed string) (string, error) {
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
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// Derive returns a 32-byte subkey for label (HKDF-SHA256, no salt).
func (b *Box) Derive(label string) []byte {
	k, err := hkdf.Key(sha256.New, b.key, nil, label, keyLen)
	if err != nil {
		panic("secretbox: hkdf: " + err.Error())
	}
	return k
}
