package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// InsertQuota stores a quota snapshot.
func (s *Store) InsertQuota(ctx context.Context, q QuotaSnapshot) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO quota_snapshots
			(account_id, at, primary_used_pct, primary_window_minutes, primary_reset_at,
			 secondary_used_pct, secondary_window_minutes, secondary_reset_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (account_id, at) DO NOTHING`,
		q.AccountID, q.At, q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt,
		q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt)
	return err
}

// LatestQuota returns the newest snapshot for an account, or ErrNotFound.
func (s *Store) LatestQuota(ctx context.Context, accountID int64) (QuotaSnapshot, error) {
	var q QuotaSnapshot
	err := s.Pool.QueryRow(ctx, `
		SELECT account_id, at, primary_used_pct, primary_window_minutes, primary_reset_at,
			secondary_used_pct, secondary_window_minutes, secondary_reset_at
		FROM quota_snapshots WHERE account_id=$1 ORDER BY at DESC LIMIT 1`, accountID).
		Scan(&q.AccountID, &q.At, &q.PrimaryUsedPct, &q.PrimaryWindowMinutes, &q.PrimaryResetAt,
			&q.SecondaryUsedPct, &q.SecondaryWindowMinutes, &q.SecondaryResetAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuotaSnapshot{}, ErrNotFound
	}
	return q, err
}

// LatestQuotas returns the newest quota snapshot of each given account.
func (s *Store) LatestQuotas(ctx context.Context, accountIDs []int64) (map[int64]QuotaSnapshot, error) {
	out := map[int64]QuotaSnapshot{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (account_id) account_id, at, primary_used_pct, primary_window_minutes, primary_reset_at,
			secondary_used_pct, secondary_window_minutes, secondary_reset_at
		FROM quota_snapshots WHERE account_id = ANY($1)
		ORDER BY account_id, at DESC`, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q QuotaSnapshot
		if err := rows.Scan(&q.AccountID, &q.At, &q.PrimaryUsedPct, &q.PrimaryWindowMinutes, &q.PrimaryResetAt,
			&q.SecondaryUsedPct, &q.SecondaryWindowMinutes, &q.SecondaryResetAt); err != nil {
			return nil, err
		}
		out[q.AccountID] = q
	}
	return out, rows.Err()
}
