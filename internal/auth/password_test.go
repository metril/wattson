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
	hashSem <- struct{}{} // occupy the semaphore so the verify must queue
	done := make(chan error, 1)
	go func() { _, err := VerifyPassword(cctx, h, "hunter2hunter2"); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("VerifyPassword did not return after ctx cancel")
	}
	<-hashSem
}

func TestVerifyStrictPHC(t *testing.T) {
	t.Parallel()
	h, err := hashWith(ctx, "hunter2hunter2", []byte("0123456789abcdef"), 8192, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(ctx, h, "hunter2hunter2"); err != nil || !ok {
		t.Fatalf("canonical hash rejected: ok=%v err=%v", ok, err)
	}
	for name, bad := range map[string]string{
		"v=19x":         strings.Replace(h, "v=19", "v=19x", 1),
		"zero padded m": strings.Replace(h, "m=8192", "m=08192", 1),
		"plus sign t":   strings.Replace(h, "t=1", "t=+1", 1),
		"trailing $":    h + "$",
		"over mem cap":  strings.Replace(h, "m=8192", "m=65537", 1),
	} {
		if ok, err := VerifyPassword(ctx, bad, "hunter2hunter2"); err == nil || ok {
			t.Errorf("%s: VerifyPassword = %v, %v; want error", name, ok, err)
		}
	}
}
