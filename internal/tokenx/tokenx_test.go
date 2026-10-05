package tokenx

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewFormat(t *testing.T) {
	tok, err := New("qen")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "qen_") {
		t.Fatalf("token %q missing qen_ prefix", tok)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token %q is not URL-safe", tok)
	}
	prefix, rest, ok := SplitPrefix(tok)
	if !ok || prefix != "qen" || rest == "" {
		t.Fatalf("SplitPrefix(%q) = %q, %q, %v", tok, prefix, rest, ok)
	}
}

func TestNewIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := New("t")
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("duplicate token generated")
		}
		seen[tok] = true
	}
}

func TestHashMatchesSHA256(t *testing.T) {
	tok := "some-token"
	sum := sha256.Sum256([]byte(tok))
	if got := Hash(tok); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("Hash = %q, want sha256 hex", got)
	}
	if Hash(tok) == Hash("other") {
		t.Fatal("distinct tokens hash identically")
	}
}

func TestSplitPrefixRejectsGarbage(t *testing.T) {
	for _, tok := range []string{"", "noprefix", "_empty", "pre_"} {
		if _, _, ok := SplitPrefix(tok); ok {
			t.Fatalf("SplitPrefix(%q) accepted", tok)
		}
	}
}
