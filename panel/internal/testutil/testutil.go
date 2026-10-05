//go:build integration

// Package testutil provides shared integration-test helpers. Tests using it
// require a running Docker daemon (testcontainers).
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"quarry.dev/quarry/panel/internal/db"
)

// StartPostgres boots an ephemeral Postgres container, applies migrations,
// and returns a pool. Cleanup runs via t.Cleanup.
func StartPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// Random per-container credentials: nothing shared or committed.
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	user := "quarry_" + hex.EncodeToString(suffix)
	pass := hex.EncodeToString(suffix) + hex.EncodeToString(suffix)

	container, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("quarry"),
		postgres.WithUsername(user),
		postgres.WithPassword(pass),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// Count is a tiny helper for assertions against the database.
func Count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

// RepoRoot returns the module root directory.
func RepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine repo root")
	}
	// testutil.go is at <root>/panel/internal/testutil/
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}
