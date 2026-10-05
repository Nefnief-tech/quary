package seal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenRoundtrip(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("top secret ca key material")

	sealed, err := box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, got) {
		t.Fatalf("got %q, want %q", got, plain)
	}
}

func TestSealUsesFreshNonces(t *testing.T) {
	box, _ := NewBox(bytes.Repeat([]byte{1}, KeySize))
	a, _ := box.Seal([]byte("same"))
	b, _ := box.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical; nonce reuse?")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	box, _ := NewBox(bytes.Repeat([]byte{2}, KeySize))
	sealed, _ := box.Seal([]byte("important"))
	sealed[len(sealed)-1] ^= 0xFF
	if _, err := box.Open(sealed); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("tampered ciphertext: err = %v, want ErrCiphertext", err)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	a, _ := NewBox(bytes.Repeat([]byte{3}, KeySize))
	b, _ := NewBox(bytes.Repeat([]byte{4}, KeySize))
	sealed, _ := a.Seal([]byte("data"))
	if _, err := b.Open(sealed); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("wrong key: err = %v, want ErrCiphertext", err)
	}
}

func TestNewBoxRejectsShortKey(t *testing.T) {
	if _, err := NewBox([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestDeriveKeyDeterministicAndBase64Aware(t *testing.T) {
	fromPassphrase, err := DeriveKey("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := DeriveKey("correct horse battery staple")
	if !bytes.Equal(fromPassphrase, again) {
		t.Fatal("derivation is not deterministic")
	}

	raw := bytes.Repeat([]byte{9}, KeySize)
	b64 := base64.StdEncoding.EncodeToString(raw)
	fromB64, err := DeriveKey(b64)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromB64, raw) {
		t.Fatal("32-byte base64 value should be used as the key verbatim")
	}

	if _, err := DeriveKey(""); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()

	key1, generated, err := LoadOrCreate(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !generated {
		t.Fatal("first call should generate a key")
	}
	info, err := os.Stat(filepath.Join(dir, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
	}

	key2, generated, err := LoadOrCreate(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if generated {
		t.Fatal("second call should load, not generate")
	}
	if !bytes.Equal(key1, key2) {
		t.Fatal("loaded key differs from generated key")
	}

	// Explicit secret wins over the key file.
	key3, _, err := LoadOrCreate(dir, "explicit secret")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(key3, key1) {
		t.Fatal("explicit secret ignored")
	}
}
