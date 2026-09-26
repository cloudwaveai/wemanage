package db_test

import (
	"context"
	"strings"
	"testing"

	"aiseat/internal/testutil"
)

func TestMigrateAndAuditImmutable(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()

	var empID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO employees (name) VALUES ('alice') RETURNING id`).Scan(&empID)
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}

	var auditID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO audit_logs (actor_employee_id, action) VALUES ($1, 'test') RETURNING id`,
		empID).Scan(&auditID)
	if err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	_, err = pool.Exec(ctx, `UPDATE audit_logs SET action = 'x' WHERE id = $1`, auditID)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only error on UPDATE, got %v", err)
	}

	_, err = pool.Exec(ctx, `DELETE FROM audit_logs WHERE id = $1`, auditID)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only error on DELETE, got %v", err)
	}
}

func TestOneActiveKeyPerEmployee(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()

	var empID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO employees (name) VALUES ('bob') RETURNING id`).Scan(&empID); err != nil {
		t.Fatalf("insert employee: %v", err)
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO api_keys (employee_id, key_hash, key_prefix) VALUES ($1, $2, 'sk-emp-a')`,
		empID, []byte("hash-a"))
	if err != nil {
		t.Fatalf("insert first key: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO api_keys (employee_id, key_hash, key_prefix) VALUES ($1, $2, 'sk-emp-b')`,
		empID, []byte("hash-b"))
	if err == nil {
		t.Fatal("expected unique-violation for second active key")
	}

	// After revoking the first, a second active key is allowed.
	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET revoked_at = now() WHERE employee_id = $1`, empID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (employee_id, key_hash, key_prefix) VALUES ($1, $2, 'sk-emp-b')`,
		empID, []byte("hash-b")); err != nil {
		t.Fatalf("insert after revoke: %v", err)
	}
}
