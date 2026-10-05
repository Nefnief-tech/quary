// Package seal encrypts sensitive material at rest (AES-256-GCM). The panel
// uses it for its CA private key. Keys come from QUARRY_SECRETS_KEY or a
// generated key file in the data dir; never hardcode keys.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const KeySize = 32

var ErrCiphertext = errors.New("ciphertext is malformed or was tampered with")

// Box seals and opens byte slices with a fixed 32-byte key.
type Box struct {
	aead cipher.AEAD
}

// NewBox constructs a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; each call uses a fresh random nonce.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < b.aead.NonceSize() {
		return nil, ErrCiphertext
	}
	nonce, body := ciphertext[:b.aead.NonceSize()], ciphertext[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, ErrCiphertext
	}
	return plaintext, nil
}

// DeriveKey turns a user-provided secret string into a 32-byte key. Values
// that already decode to 32 bytes of base64 are used as-is, anything else is
// hashed (so passphrases of any length work).
func DeriveKey(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("secret is empty")
	}
	if raw, err := base64.StdEncoding.DecodeString(secret); err == nil && len(raw) == KeySize {
		return raw, nil
	}
	if raw, err := base64.RawURLEncoding.DecodeString(secret); err == nil && len(raw) == KeySize {
		return raw, nil
	}
	sum := sha256.Sum256([]byte(secret))
	key := make([]byte, KeySize)
	copy(key, sum[:])
	return key, nil
}

// LoadOrCreate resolves the sealing key: the explicit secret wins, otherwise
// the key file in dataDir is read, otherwise a random key is generated and
// persisted (file mode 0600). It reports whether a key was generated so the
// caller can warn that the key only lives in the data dir.
func LoadOrCreate(dataDir, secret string) (key []byte, generated bool, err error) {
	if secret != "" {
		key, err := DeriveKey(secret)
		return key, false, err
	}

	path := filepath.Join(dataDir, "secrets.key")
	if raw, err := os.ReadFile(path); err == nil {
		key, err := DeriveKey(string(raw))
		return key, false, err
	} else if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("read key file: %w", err)
	}

	key = make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, false, fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create data dir: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, false, fmt.Errorf("write key file: %w", err)
	}
	return key, true, nil
}
