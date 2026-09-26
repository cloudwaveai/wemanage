// Package testutil provides a per-test isolated PostgreSQL schema.
package testutil

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"aiseat/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDBURL returns the configured test database URL or the local default.
func TestDBURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable"
}

// NewDB creates an isolated schema, migrates it, and returns a pool scoped to it.
// The schema is dropped when the test finishes.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := TestDBURL()
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("test database unreachable (%v); run `make test-db-up`", err)
	}

	schema := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(context.Background())
	})
	return pool
}
