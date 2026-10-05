//go:build integration

package db_test

import (
	"context"
	"testing"

	"quarry.dev/quarry/panel/internal/db"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/testutil"
)

// TestMigrationsIdempotentAndReversible walks the schema up, down and up
// again against a real Postgres; every migration must be reversible.
func TestMigrationsIdempotentAndReversible(t *testing.T) {
	ctx := context.Background()
	pool := testutil.StartPostgres(t)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate (idempotency): %v", err)
	}

	if err := db.MigrateDown(ctx, pool); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if n := testutil.Count(t, pool, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`); n != 0 {
		t.Fatalf("%d tables left after down, want 0 (excluding goose's tracker)", n)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate up after down: %v", err)
	}
}

func TestSeedRolesAndPermissions(t *testing.T) {
	ctx := context.Background()
	pool := testutil.StartPostgres(t)
	q := gen.New(pool)

	for _, name := range []string{"admin", "user"} {
		if _, err := q.GetRoleByName(ctx, name); err != nil {
			t.Fatalf("seeded role %q missing: %v", name, err)
		}
	}

	admin, err := q.GetRoleByName(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	perms, err := q.PermissionsForRole(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range perms {
		if p == "*" {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin role lacks wildcard permission, has %v", perms)
	}
}
