package secretbox

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// SealingKey recomputes the AEAD key so tests can compare Derive outputs to it.
func SealingKey(b *Box) []byte {
	k, err := hkdf.Key(sha256.New, b.key, nil, aeadLabel, keyLen)
	if err != nil {
		panic(err)
	}
	return k
}
