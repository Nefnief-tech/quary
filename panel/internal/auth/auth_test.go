package auth

import (
	"testing"
	"time"
)

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Error("correct password must verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Error("wrong password must not verify")
	}
	if VerifyPassword("", hash) {
		t.Error("empty password must not verify")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password accepted")
	}
}

func TestLoginLimiterBlocksAfterBurst(t *testing.T) {
	l := NewLoginLimiter(time.Minute, 3)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4|alice") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("1.2.3.4|alice") {
		t.Fatal("attempt 4 should be blocked")
	}
	// Different key is unaffected.
	if !l.Allow("1.2.3.4|bob") {
		t.Fatal("different key should be allowed")
	}
}

func TestLoginLimiterReset(t *testing.T) {
	l := NewLoginLimiter(time.Minute, 1)
	if !l.Allow("k") {
		t.Fatal("first attempt allowed")
	}
	if l.Allow("k") {
		t.Fatal("second attempt blocked")
	}
	l.Reset("k")
	if !l.Allow("k") {
		t.Fatal("reset must restore allowance")
	}
}
