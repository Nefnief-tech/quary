// Package auth implements the panel's authentication primitives: Argon2id
// password hashing, opaque session tokens, and scoped API keys. Tokens are
// random values; only SHA-256 hashes are stored.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	argon2id "github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"quarry.dev/quarry/internal/tokenx"
	"quarry.dev/quarry/panel/internal/db/gen"
)

const (
	SessionPrefix = "qus"
	APIKeyPrefix  = "qpk"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionExpired     = errors.New("session expired")
	ErrAPIKeyInvalid      = errors.New("api key invalid")
	ErrAPIKeyExpired      = errors.New("api key expired")
)

// HashPassword derives an Argon2id PHC string for storage.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is empty")
	}
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

// VerifyPassword checks a plaintext password against a stored PHC string.
func VerifyPassword(password, hash string) bool {
	match, err := argon2id.ComparePasswordAndHash(password, hash)
	return err == nil && match
}

// Identity is what the API middleware puts into the request context.
type Identity struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
	Username  string
	Email     *string
	RoleID    uuid.UUID
	RoleName  string
	Disabled  bool
	Perms     []string
}

// Sessions manages opaque session tokens backed by the sessions table.
type Sessions struct {
	q        *gen.Queries
	lifetime time.Duration
}

func NewSessions(q *gen.Queries, lifetime time.Duration) *Sessions {
	return &Sessions{q: q, lifetime: lifetime}
}

// Create mints a session for a user; the returned token is handed to the
// client exactly once (as a cookie) — only its hash is stored.
func (s *Sessions) Create(ctx context.Context, userID uuid.UUID, ip, userAgent string) (token string, expires time.Time, err error) {
	token, err = tokenx.New(SessionPrefix)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}
	expires = time.Now().Add(s.lifetime)
	if _, err := s.q.CreateSession(ctx, gen.CreateSessionParams{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: tokenx.Hash(token),
		Ip:        ip,
		UserAgent: userAgent,
		ExpiresAt: expires,
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("store session: %w", err)
	}
	return token, expires, nil
}

// Resolve returns the session's identity if the token is valid and unexpired.
func (s *Sessions) Resolve(ctx context.Context, token string) (*Identity, error) {
	row, err := s.q.SessionByIdentityByTokenHash(ctx, tokenx.Hash(token))
	if err != nil {
		return nil, ErrSessionExpired
	}
	if row.Disabled {
		return nil, ErrSessionExpired
	}
	perms, err := s.q.PermissionsForRole(ctx, row.RoleID)
	if err != nil {
		return nil, fmt.Errorf("load permissions: %w", err)
	}
	return &Identity{
		SessionID: row.SessionID,
		UserID:    row.UserID,
		Username:  row.Username,
		Email:     row.Email,
		RoleID:    row.RoleID,
		RoleName:  row.RoleName,
		Disabled:  row.Disabled,
		Perms:     perms,
	}, nil
}

// Delete removes a session (logout).
func (s *Sessions) Delete(ctx context.Context, sessionID uuid.UUID) error {
	return s.q.DeleteSession(ctx, sessionID)
}
