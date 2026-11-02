// Package auth holds credential primitives: password hashing today, session
// and token helpers elsewhere.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/argon2"
)

// Hashing parameters for new hashes (OWASP argon2id minimum: 12 MiB, t=3, p=1).
const (
	argonMemKiB  = 12288
	argonTime    = 3
	argonThreads = 1
	saltLen      = 16
	keyLen       = 32

	minPasswordLen = 10
	maxPasswordLen = 256

	// Upper bounds on parameters accepted when verifying, so a corrupt or
	// hostile stored hash cannot request gigabytes of memory.
	maxMemKiB  = 64 * 1024
	maxTime    = 16
	maxThreads = 16
)

var (
	errMalformed = errors.New("auth: malformed password hash")

	// hashSem caps concurrent argon2 computations at one, bounding the
	// memory the server spends on hashing (12 MiB) regardless of load.
	hashSem = make(chan struct{}, 1)

	b64 = base64.RawStdEncoding
)

// HashPassword returns a PHC-format argon2id hash of pw with a random salt:
// $argon2id$v=19$m=12288,t=3,p=1$<salt>$<hash> (raw standard base64).
// ctx bounds the wait for the hashing semaphore.
func HashPassword(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	return hashWith(ctx, pw, salt, argonMemKiB, argonTime, argonThreads)
}

func hashWith(ctx context.Context, pw string, salt []byte, m, t uint32, p uint8) (string, error) {
	key, err := derive(ctx, pw, salt, m, t, p, keyLen)
	if err != nil {
		return "", err
	}
	return formatPHC(m, t, p, salt, key), nil
}

func formatPHC(m, t uint32, p uint8, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// queuedKey carries an optional test hook, see export_test.go.
type queuedKey struct{}

// derive runs argon2id under the global semaphore. Waiting for the semaphore
// honours ctx, so a cancelled request leaves the queue instead of burning
// 12 MiB and ~50 ms of CPU for nobody.
func derive(ctx context.Context, pw string, salt []byte, m, t uint32, p uint8, n uint32) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f, ok := ctx.Value(queuedKey{}).(func()); ok {
		f() // test hook: the caller is about to block on the semaphore
	}
	select {
	case hashSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSem }()
	key := argon2.IDKey([]byte(pw), salt, t, m, p, n)
	scheduleFree()
	return key, nil
}

var freeScheduled atomic.Bool

// scheduleFree hands the 12 MiB argon2 work area back to the OS shortly after
// a hash (one call covers a burst of them): the idle RSS budget (30 MB) has no
// room for memory the runtime keeps for reuse.
func scheduleFree() {
	if freeScheduled.CompareAndSwap(false, true) {
		time.AfterFunc(time.Second, func() {
			freeScheduled.Store(false)
			debug.FreeOSMemory()
		})
	}
}

// VerifyPassword reports whether pw matches the PHC hash, using the
// parameters recorded in the hash. A malformed hash is an error, not false.
// ctx bounds the wait for the hashing semaphore; on cancellation the error
// wraps ctx.Err().
func VerifyPassword(ctx context.Context, phc, pw string) (bool, error) {
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errMalformed
	}
	var version int
	if n, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || n != 1 || version != argon2.Version {
		return false, errMalformed
	}
	var m, t uint32
	var p uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || n != 3 {
		return false, errMalformed
	}
	if m == 0 || m > maxMemKiB || t == 0 || t > maxTime || p == 0 || p > maxThreads {
		return false, errMalformed
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, errMalformed
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errMalformed
	}
	// Strict PHC: the hash must be byte-identical to the canonical encoding
	// of what was parsed (no trailing junk, leading zeros or odd base64).
	if phc != formatPHC(m, t, p, salt, want) {
		return false, errMalformed
	}
	got, err := derive(ctx, pw, salt, m, t, p, uint32(len(want)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ValidatePassword enforces the length policy: 10 to 256 bytes.
func ValidatePassword(pw string) error {
	if len(pw) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	return nil
}
