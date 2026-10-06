// Package settings stores panel-level configuration (tunnel providers) in
// the settings key/value table. Secrets (frps auth tokens) are sealed with
// the panel key before storage; they are never returned by the API.
package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"quarry.dev/quarry/internal/seal"
	gen "quarry.dev/quarry/panel/internal/db/gen"
)

// frpKey is the settings row holding frp tunnel provider configuration.
const frpKey = "expose.frp"

// FrpConfig is the frp tunnel provider configuration.
type FrpConfig struct {
	Enabled    bool   `json:"enabled"`
	ServerAddr string `json:"server_addr"`
	ServerPort int32  `json:"server_port"`
	// Token is the frps auth token; sealed at rest.
	Token string `json:"-"`
}

// Service reads and writes provider settings.
type Service struct {
	pool *pgxpool.Pool
	box  *seal.Box
}

func NewService(pool *pgxpool.Pool, sealKey []byte) (*Service, error) {
	box, err := seal.NewBox(sealKey)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, box: box}, nil
}

// Frp returns the frp provider config; a missing row yields defaults.
func (s *Service) Frp(ctx context.Context) (FrpConfig, error) {
	q := gen.New(s.pool)
	raw, err := q.GetSetting(ctx, frpKey)
	if err != nil {
		return FrpConfig{}, nil // not configured yet
	}
	var stored struct {
		Enabled    bool   `json:"enabled"`
		ServerAddr string `json:"server_addr"`
		ServerPort int32  `json:"server_port"`
		Sealed     string `json:"sealed"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return FrpConfig{}, fmt.Errorf("corrupt frp settings: %w", err)
	}
	cfg := FrpConfig{
		Enabled:    stored.Enabled,
		ServerAddr: stored.ServerAddr,
		ServerPort: stored.ServerPort,
	}
	if stored.Sealed != "" {
		ciphertext, err := base64.StdEncoding.DecodeString(stored.Sealed)
		if err != nil {
			return FrpConfig{}, errors.New("frp token unreadable (bad encoding)")
		}
		plain, err := s.box.Open(ciphertext)
		if err != nil {
			return FrpConfig{}, errors.New("frp token unreadable (seal key changed?)")
		}
		cfg.Token = string(plain)
	}
	return cfg, nil
}

// SetFrp stores the frp provider config. An empty Token keeps the
// previously stored one.
func (s *Service) SetFrp(ctx context.Context, cfg FrpConfig) error {
	if cfg.Token == "" {
		// Keep the previously stored token; a corrupt row (e.g. written by
		// an older encoding) just yields an empty token and is overwritten.
		prev, _ := s.Frp(ctx)
		cfg.Token = prev.Token
	}
	sealed, err := s.box.Seal([]byte(cfg.Token))
	if err != nil {
		return err
	}
	value, _ := json.Marshal(map[string]any{
		"enabled":     cfg.Enabled,
		"server_addr": cfg.ServerAddr,
		"server_port": cfg.ServerPort,
		// Raw ciphertext is binary; store it base64-encoded for JSON.
		"sealed": base64.StdEncoding.EncodeToString(sealed),
	})
	q := gen.New(s.pool)
	return q.UpsertSetting(ctx, gen.UpsertSettingParams{Key: frpKey, Value: value})
}
