package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func TestHashFormat(t *testing.T) {
	t.Parallel()
	h, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=12288,t=3,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !re.MatchString(h) {
		t.Fatalf("hash %q does not match PHC format", h)
	}
	h2, _ := HashPassword(ctx, "correct horse")
	if h == h2 {
		t.Fatal("two hashes of the same password are identical (salt not random)")
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	h, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(ctx, h, "correct horse"); err != nil || !ok {
		t.Fatalf("right password: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword(ctx, h, "wrong horse!"); err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{
		"$argon2id$garbage", "", "plain",
		"$argon2i$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=18$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=0,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$!!!$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$",
		"$argon2id$v=19x$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1x$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=08192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=65537,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA$",
		"$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAB$AAAA",
	} {
		if ok, err := VerifyPassword(ctx, bad, "x"); err == nil || ok {
			t.Errorf("VerifyPassword(%q) = %v, %v; want error", bad, ok, err)
		}
	}
}

func TestVerifyHonoursParams(t *testing.T) {
	t.Parallel()
	h, err := hashWith(ctx, "hunter2hunter2", []byte("0123456789abcdef"), 8192, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected hash %q", h)
	}
	if ok, err := VerifyPassword(ctx, h, "hunter2hunter2"); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword(ctx, h, "hunter2hunter3"); ok {
		t.Fatal("wrong password verified")
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()
	if err := ValidatePassword(strings.Repeat("a", 10)); err != nil {
		t.Fatalf("10 chars: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 256)); err != nil {
		t.Fatalf("256 chars: %v", err)
	}
	if ValidatePassword(strings.Repeat("a", 9)) == nil {
		t.Fatal("9 chars accepted")
	}
	if ValidatePassword(strings.Repeat("a", 257)) == nil {
		t.Fatal("257 chars accepted")
	}
}

func TestVerifyCtxCancelWhileQueued(t *testing.T) {
	t.Parallel()
	h, err := hashWith(ctx, "hunter2hunter2", []byte("0123456789abcdef"), 8192, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queued := make(chan struct{})
	cctx = QueuedHook(cctx, func() { close(queued) })
	hashSem <- struct{}{} // occupy the semaphore so the verify must queue
	defer func() { <-hashSem }()
	done := make(chan error, 1)
	go func() { _, err := VerifyPassword(cctx, h, "hunter2hunter2"); done <- err }()
	select {
	case <-queued: // goroutine is past the early ctx check and about to block
	case <-time.After(2 * time.Second):
		t.Fatal("verify never reached the semaphore")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("VerifyPassword did not return after ctx cancel")
	}
}
