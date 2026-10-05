package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/internal/tokenx"
	"quarry.dev/quarry/panel/internal/db/gen"
)

// APIKeys manages scoped API keys. The full key is
// "qpk_<lookup>.<secret>" — '.' separates because base64url never emits it.
// The lookup part is stored in the database for O(1) lookup; only the
// SHA-256 of the full key is stored.
type APIKeys struct {
	q *gen.Queries
}

func NewAPIKeys(q *gen.Queries) *APIKeys {
	return &APIKeys{q: q}
}

// Create mints a key for a user. scopes must be validated by the caller
// against the RBAC catalog.
func (k *APIKeys) Create(ctx context.Context, userID uuid.UUID, name string, scopes []string, expires *time.Time) (fullKey string, row gen.ApiKey, err error) {
	lookup, err := tokenx.New(APIKeyPrefix)
	if err != nil {
		return "", gen.ApiKey{}, fmt.Errorf("generate api key: %w", err)
	}
	secret, err := tokenx.New("secret")
	if err != nil {
		return "", gen.ApiKey{}, fmt.Errorf("generate api key: %w", err)
	}
	fullKey = lookup + "." + secret
	row, err = k.q.CreateAPIKey(ctx, gen.CreateAPIKeyParams{
		ID:         uuid.New(),
		UserID:     userID,
		Name:       name,
		Prefix:     lookup,
		SecretHash: tokenx.Hash(fullKey),
		Scopes:     scopes,
		ExpiresAt:  expires,
	})
	if err != nil {
		return "", gen.ApiKey{}, fmt.Errorf("store api key: %w", err)
	}
	return fullKey, row, nil
}

// Resolve authenticates a presented key and returns its row (with scopes).
func (k *APIKeys) Resolve(ctx context.Context, presented string) (gen.ApiKey, error) {
	if !strings.HasPrefix(presented, APIKeyPrefix+"_") {
		return gen.ApiKey{}, ErrAPIKeyInvalid
	}
	dot := strings.LastIndexByte(presented, '.')
	if dot < len(APIKeyPrefix)+2 {
		return gen.ApiKey{}, ErrAPIKeyInvalid
	}
	row, err := k.q.GetAPIKeyByPrefix(ctx, presented[:dot])
	if err != nil {
		return gen.ApiKey{}, ErrAPIKeyInvalid
	}
	if subtle.ConstantTimeCompare([]byte(tokenx.Hash(presented)), []byte(row.SecretHash)) != 1 {
		return gen.ApiKey{}, ErrAPIKeyInvalid
	}
	if row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now()) {
		return gen.ApiKey{}, ErrAPIKeyExpired
	}
	return row, nil
}

// Touch records usage. Errors are ignored by callers (best-effort).
func (k *APIKeys) Touch(ctx context.Context, id uuid.UUID) {
	_ = k.q.TouchAPIKey(ctx, id)
}
