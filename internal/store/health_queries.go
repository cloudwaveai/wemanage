package store

import (
	"context"
)

// RecentReasoning returns the most recent reasoning-token counts for an account
// from requests that used a medium/high reasoning effort (newest first).
func (s *Store) RecentReasoning(ctx context.Context, accountID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT tokens_reasoning FROM requests
		WHERE account_id=$1 AND reasoning_effort IN ('medium','high') AND tokens_reasoning > 0
		ORDER BY started_at DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AllAccounts returns the ids of every account (for periodic sweeps).
func (s *Store) AllAccountIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM upstream_accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
