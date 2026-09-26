package store

import (
	"context"
	"encoding/json"
	"time"
)

// AddSignal records a new account health signal (open by default).
func (s *Store) AddSignal(ctx context.Context, accountID int64, kind string, detail map[string]any) (Signal, error) {
	if detail == nil {
		detail = map[string]any{}
	}
	d, _ := json.Marshal(detail)
	var sig Signal
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO account_signals (account_id, kind, detail)
		VALUES ($1,$2,$3)
		RETURNING id, account_id, kind, at, resolved_at, notified_at`,
		accountID, kind, d).
		Scan(&sig.ID, &sig.AccountID, &sig.Kind, &sig.At, &sig.ResolvedAt, &sig.NotifiedAt)
	sig.Detail = detail
	return sig, err
}

// OpenSignals returns unresolved signals for an account.
func (s *Store) OpenSignals(ctx context.Context, accountID int64) ([]Signal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, account_id, kind, detail, at, resolved_at, notified_at
		FROM account_signals WHERE account_id=$1 AND resolved_at IS NULL ORDER BY at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSignals(rows)
}

// HasOpenSignal reports whether an unresolved signal of the given kind exists.
func (s *Store) HasOpenSignal(ctx context.Context, accountID int64, kind string) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM account_signals WHERE account_id=$1 AND kind=$2 AND resolved_at IS NULL`,
		accountID, kind).Scan(&n)
	return n > 0, err
}

// CountSignalsSince counts signals of a kind created since t.
func (s *Store) CountSignalsSince(ctx context.Context, accountID int64, kind string, since time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM account_signals WHERE account_id=$1 AND kind=$2 AND at >= $3`,
		accountID, kind, since).Scan(&n)
	return n, err
}

// ResolveSignal marks a signal resolved now.
func (s *Store) ResolveSignal(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE account_signals SET resolved_at=now() WHERE id=$1 AND resolved_at IS NULL`, id)
	return err
}

// ResolveSignalsOfKind resolves all open signals of a kind for an account.
func (s *Store) ResolveSignalsOfKind(ctx context.Context, accountID int64, kind string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE account_signals SET resolved_at=now() WHERE account_id=$1 AND kind=$2 AND resolved_at IS NULL`, accountID, kind)
	return err
}

// UnnotifiedSignals returns open signals that have not yet been notified.
func (s *Store) UnnotifiedSignals(ctx context.Context) ([]Signal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, account_id, kind, detail, at, resolved_at, notified_at
		FROM account_signals WHERE resolved_at IS NULL AND notified_at IS NULL ORDER BY at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSignals(rows)
}

// MarkNotified stamps notified_at.
func (s *Store) MarkNotified(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE account_signals SET notified_at=now() WHERE id=$1`, id)
	return err
}

func scanSignals(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Signal, error) {
	var out []Signal
	for rows.Next() {
		var sig Signal
		var d []byte
		if err := rows.Scan(&sig.ID, &sig.AccountID, &sig.Kind, &d, &sig.At, &sig.ResolvedAt, &sig.NotifiedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &sig.Detail)
		out = append(out, sig)
	}
	return out, rows.Err()
}
