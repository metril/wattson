package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const (
	secretBytes = 32 // 256 bits from crypto/rand
	tokenPrefix = "wat_"
	prefixLen   = 12 // chars of the plaintext kept for display
)

var b64url = base64.RawURLEncoding

func randomSecret() string {
	b := make([]byte, secretBytes)
	rand.Read(b) // never returns an error; crashes the program if the OS RNG fails (Go 1.24+)
	return b64url.EncodeToString(b)
}

// GenerateSecret returns a session secret: 32 random bytes as base64url (43
// chars). Only hash (hex SHA-256 of plain) is ever stored.
func GenerateSecret() (plain, hash string) {
	plain = randomSecret()
	return plain, HashSecret(plain)
}

// GenerateToken returns an API token "wat_" + 43 base64url chars (47 chars,
// 256 bits of entropy). prefix is the first 12 characters, safe to display;
// hash is what the store keeps.
func GenerateToken() (plain, prefix, hash string) {
	plain = tokenPrefix + randomSecret()
	return plain, plain[:prefixLen], HashSecret(plain)
}

// HashSecret returns hex(sha256(plain)). Session and token secrets are
// high-entropy random values, so a fast unsalted hash is appropriate.
func HashSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
