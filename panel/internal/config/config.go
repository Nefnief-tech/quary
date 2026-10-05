// Package config loads panel configuration from the environment (QUARRY_*
// variables). Credentials never have defaults; a missing database URL is a
// startup error, not a hardcoded fallback.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	GRPCAddr        string
	DataDir         string
	SecretsKey      string        // optional; generated into DataDir when empty
	SessionLifetime time.Duration // cookie + server-side session TTL
	Env             string        // "dev" or "production"
	TLSDNSNames     []string      // SANs on the gRPC serving certificate
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:     os.Getenv("QUARRY_DATABASE_URL"),
		HTTPAddr:        envOr("QUARRY_HTTP_ADDR", ":8080"),
		GRPCAddr:        envOr("QUARRY_GRPC_ADDR", ":9010"),
		DataDir:         envOr("QUARRY_DATA_DIR", "./data"),
		SecretsKey:      os.Getenv("QUARRY_SECRETS_KEY"),
		SessionLifetime: 24 * time.Hour,
		Env:             envOr("QUARRY_ENV", "dev"),
	}

	if c.DatabaseURL == "" {
		return c, fmt.Errorf("QUARRY_DATABASE_URL is required (e.g. postgres://user:pass@host:5432/quarry)")
	}

	if v := os.Getenv("QUARRY_SESSION_LIFETIME"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("QUARRY_SESSION_LIFETIME: %w", err)
		}
		c.SessionLifetime = d
	}

	for _, name := range strings.Split(envOr("QUARRY_TLS_DNS_NAMES", "localhost"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			c.TLSDNSNames = append(c.TLSDNSNames, name)
		}
	}

	if c.Env != "dev" && c.Env != "production" {
		return c, fmt.Errorf("QUARRY_ENV must be \"dev\" or \"production\", got %q", c.Env)
	}
	return c, nil
}

// Production reports whether hardening (Secure cookies, strict errors)
// should apply.
func (c Config) Production() bool { return c.Env == "production" }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
