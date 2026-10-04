package store

import (
	"context"
	"errors"

	"aiseat/internal/secure"

	"github.com/jackc/pgx/v5"
)

// IssueKey revokes any existing active key for the employee and issues a new one
// in a single transaction. Returns the plaintext key (shown once) and its record.
func (s *Store) IssueKey(ctx context.Context, employeeID int64) (plain string, key APIKey, err error) {
	plain, hash, prefix := secure.NewAPIKey()
	err = s.tx(ctx, func(q queryer) error {
		if _, e := q.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE employee_id=$1 AND revoked_at IS NULL`, employeeID); e != nil {
			return e
		}
		row := q.QueryRow(ctx, `
			INSERT INTO api_keys (employee_id, key_hash, key_prefix)
			VALUES ($1,$2,$3)
			RETURNING id, employee_id, key_prefix, created_at, revoked_at, last_used_at`,
			employeeID, hash, prefix)
		return row.Scan(&key.ID, &key.EmployeeID, &key.KeyPrefix, &key.CreatedAt, &key.RevokedAt, &key.LastUsedAt)
	})
	if err != nil {
		return "", APIKey{}, err
	}
	return plain, key, nil
}

// RevokeKeys revokes all active keys for an employee.
func (s *Store) RevokeKeys(ctx context.Context, employeeID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE employee_id=$1 AND revoked_at IS NULL`, employeeID)
	return err
}

// ActiveKey returns the employee's active key record, or ErrNotFound.
func (s *Store) ActiveKey(ctx context.Context, employeeID int64) (APIKey, error) {
	var k APIKey
	err := s.Pool.QueryRow(ctx, `
		SELECT id, employee_id, key_prefix, created_at, revoked_at, last_used_at
		FROM api_keys WHERE employee_id=$1 AND revoked_at IS NULL`, employeeID).
		Scan(&k.ID, &k.EmployeeID, &k.KeyPrefix, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	return k, err
}

// LookupKey resolves an active key hash to its principal (employee + accounts).
func (s *Store) LookupKey(ctx context.Context, hash []byte) (KeyPrincipal, error) {
	var kp KeyPrincipal
	var openID *string
	err := s.Pool.QueryRow(ctx, `
		SELECT k.id, e.id, e.feishu_open_id, e.status, (e.notice_ack_at IS NOT NULL), e.notice_ack_version,
			(e.frozen_at IS NOT NULL), e.frozen_reason, e.remote_allowed, e.is_service
		FROM api_keys k
		JOIN employees e ON e.id = k.employee_id
		WHERE k.key_hash=$1 AND k.revoked_at IS NULL`, hash).
		Scan(&kp.KeyID, &kp.EmployeeID, &openID, &kp.EmployeeStatus, &kp.NoticeAcked, &kp.NoticeAckVersion,
			&kp.Frozen, &kp.FrozenReason, &kp.RemoteAllowed, &kp.IsService)
	if openID != nil {
		kp.EmployeeOpenID = *openID
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return KeyPrincipal{}, ErrNotFound
	}
	if err != nil {
		return KeyPrincipal{}, err
	}
	if kp.Accounts, err = s.AccountsOf(ctx, kp.EmployeeID); err != nil {
		return KeyPrincipal{}, err
	}
	return kp, nil
}

// TouchKey updates last_used_at (best-effort, ignores missing).
func (s *Store) TouchKey(ctx context.Context, keyID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, keyID)
	return err
}

// RevokedKeyOwner finds whose key a revoked key hash was, so a request still
// using an old key can be explained to (and logged for) its owner.
func (s *Store) RevokedKeyOwner(ctx context.Context, hash []byte) (keyID, employeeID int64, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT k.id, k.employee_id FROM api_keys k
		WHERE k.key_hash=$1 AND k.revoked_at IS NOT NULL
		ORDER BY k.revoked_at DESC LIMIT 1`, hash).Scan(&keyID, &employeeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return keyID, employeeID, err
}
