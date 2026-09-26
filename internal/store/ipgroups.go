package store

import (
	"context"
	"errors"

	"aiseat/internal/secure"

	"github.com/jackc/pgx/v5"
)

// CreateIPGroup stores a new IP group with its proxy URL encrypted at rest.
func (s *Store) CreateIPGroup(ctx context.Context, box *secure.Box, name, proxyURL string, capacity int) (IPGroup, error) {
	if capacity <= 0 {
		capacity = 10
	}
	enc := box.Seal([]byte(proxyURL))
	var g IPGroup
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO ip_groups (name, proxy_url_enc, capacity)
		VALUES ($1,$2,$3)
		RETURNING id, name, capacity, expected_egress_ip, last_egress_ip, last_check_error, last_checked_at`,
		name, enc, capacity).
		Scan(&g.ID, &g.Name, &g.Capacity, &g.ExpectedEgressIP, &g.LastEgressIP, &g.LastCheckError, &g.LastCheckedAt)
	if err != nil {
		return IPGroup{}, err
	}
	g.ProxyURL = proxyURL
	return g, nil
}

// GetIPGroup returns one group with its proxy URL decrypted.
func (s *Store) GetIPGroup(ctx context.Context, box *secure.Box, id int64) (IPGroup, error) {
	var g IPGroup
	var enc []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT g.id, g.name, g.proxy_url_enc, g.capacity, g.expected_egress_ip, g.last_egress_ip, g.last_check_error, g.last_checked_at,
			(SELECT COUNT(*) FROM upstream_accounts a WHERE a.ip_group_id = g.id)
		FROM ip_groups g WHERE g.id=$1`, id).
		Scan(&g.ID, &g.Name, &enc, &g.Capacity, &g.ExpectedEgressIP, &g.LastEgressIP, &g.LastCheckError, &g.LastCheckedAt, &g.AccountCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return IPGroup{}, ErrNotFound
	}
	if err != nil {
		return IPGroup{}, err
	}
	plain, err := box.Open(enc)
	if err != nil {
		return IPGroup{}, err
	}
	g.ProxyURL = string(plain)
	return g, nil
}

// ListIPGroups returns all groups with decrypted proxy URLs and account counts.
func (s *Store) ListIPGroups(ctx context.Context, box *secure.Box) ([]IPGroup, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id, g.name, g.proxy_url_enc, g.capacity, g.expected_egress_ip, g.last_egress_ip, g.last_check_error, g.last_checked_at,
			(SELECT COUNT(*) FROM upstream_accounts a WHERE a.ip_group_id = g.id)
		FROM ip_groups g ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPGroup
	for rows.Next() {
		var g IPGroup
		var enc []byte
		if err := rows.Scan(&g.ID, &g.Name, &enc, &g.Capacity, &g.ExpectedEgressIP, &g.LastEgressIP, &g.LastCheckError, &g.LastCheckedAt, &g.AccountCount); err != nil {
			return nil, err
		}
		if plain, err := box.Open(enc); err == nil {
			g.ProxyURL = string(plain)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateIPGroup updates capacity and/or proxy URL. Empty proxyURL leaves it unchanged.
func (s *Store) UpdateIPGroup(ctx context.Context, box *secure.Box, id int64, capacity int, proxyURL string) error {
	if proxyURL != "" {
		enc := box.Seal([]byte(proxyURL))
		_, err := s.Pool.Exec(ctx, `UPDATE ip_groups SET capacity=$2, proxy_url_enc=$3 WHERE id=$1`, id, capacity, enc)
		return err
	}
	_, err := s.Pool.Exec(ctx, `UPDATE ip_groups SET capacity=$2 WHERE id=$1`, id, capacity)
	return err
}

// RecordEgressCheck stores the observed egress IP and any error for a group.
func (s *Store) RecordEgressCheck(ctx context.Context, id int64, ip, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE ip_groups SET last_egress_ip=$2, last_check_error=$3, last_checked_at=now(),
			expected_egress_ip = CASE WHEN expected_egress_ip='' AND $3='' THEN $2 ELSE expected_egress_ip END
		WHERE id=$1`, id, ip, errMsg)
	return err
}
