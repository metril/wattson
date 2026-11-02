package auth

import (
	"regexp"
	"strings"
	"testing"
)

func TestHashFormat(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=12288,t=3,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !re.MatchString(h) {
		t.Fatalf("hash %q does not match PHC format", h)
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("two hashes of the same password are identical (salt not random)")
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(h, "correct horse"); err != nil || !ok {
		t.Fatalf("right password: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword(h, "wrong horse!"); err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{
		"$argon2id$garbage", "", "plain",
		"$argon2i$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=18$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=0,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$!!!$AAAA",
		"$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$",
	} {
		if ok, err := VerifyPassword(bad, "x"); err == nil || ok {
			t.Errorf("VerifyPassword(%q) = %v, %v; want error", bad, ok, err)
		}
	}
}

func TestVerifyHonoursParams(t *testing.T) {
	t.Parallel()
	h := hashWith("hunter2hunter2", []byte("0123456789abcdef"), 8192, 1, 1)
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected hash %q", h)
	}
	if ok, err := VerifyPassword(h, "hunter2hunter2"); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "hunter2hunter3"); ok {
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
