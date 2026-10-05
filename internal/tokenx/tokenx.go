// Package tokenx generates and hashes the opaque tokens the panel hands out
// (sessions, API keys, node enrollment tokens). Tokens are random 32-byte
// values; only SHA-256 hashes are ever persisted.
package tokenx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// New returns a prefixed opaque token: "<prefix>_<base64url(32 random bytes)>".
func New(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Hash is the storage form of a token (hex-encoded SHA-256).
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SplitPrefix returns the "qp_"-style prefix and the rest of the token.
// For API keys the prefix doubles as the lookup key stored in the database.
func SplitPrefix(token string) (prefix, rest string, ok bool) {
	prefix, rest, found := strings.Cut(token, "_")
	if !found || prefix == "" || rest == "" {
		return "", "", false
	}
	return prefix, rest, true
}
