package store

import (
	"context"
	"time"
)

// Subscription is a ChatGPT account's subscription: when it next renews (or
// ends, when cancelled), as of CheckedAt. Error is the last check's failure,
// kept alongside the last good values.
type Subscription struct {
	AccountID  int64      `json:"-"`
	Plan       string     `json:"plan"`
	Active     bool       `json:"active"`
	RenewsAt   *time.Time `json:"renews_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CancelsAt  *time.Time `json:"cancels_at"`
	Delinquent bool       `json:"delinquent"`
	CheckedAt  time.Time  `json:"checked_at"`
	Error      string     `json:"error,omitempty"`
}

// SaveSubscription records a successful check.
func (s *Store) SaveSubscription(ctx context.Context, sub Subscription) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO account_subscriptions (account_id, plan, active, renews_at, expires_at, cancels_at, delinquent, checked_at, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now(),'')
		ON CONFLICT (account_id) DO UPDATE SET plan=EXCLUDED.plan, active=EXCLUDED.active, renews_at=EXCLUDED.renews_at,
			expires_at=EXCLUDED.expires_at, cancels_at=EXCLUDED.cancels_at, delinquent=EXCLUDED.delinquent,
			checked_at=now(), error=''`,
		sub.AccountID, sub.Plan, sub.Active, sub.RenewsAt, sub.ExpiresAt, sub.CancelsAt, sub.Delinquent)
	return err
}

// SubscriptionCheckFailed notes a failed check, keeping the last good values
// (or, for an account never checked, the fallback's).
func (s *Store) SubscriptionCheckFailed(ctx context.Context, fallback Subscription, msg string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO account_subscriptions (account_id, plan, active, expires_at, checked_at, error)
		VALUES ($1,$2,$3,$4,now(),$5)
		ON CONFLICT (account_id) DO UPDATE SET checked_at=now(), error=EXCLUDED.error`,
		fallback.AccountID, fallback.Plan, fallback.Active, fallback.ExpiresAt, msg)
	return err
}

// Subscriptions returns every known subscription by account id.
func (s *Store) Subscriptions(ctx context.Context) (map[int64]Subscription, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT account_id, plan, active, renews_at, expires_at, cancels_at, delinquent, checked_at, error
		FROM account_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Subscription{}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.AccountID, &sub.Plan, &sub.Active, &sub.RenewsAt, &sub.ExpiresAt, &sub.CancelsAt,
			&sub.Delinquent, &sub.CheckedAt, &sub.Error); err != nil {
			return nil, err
		}
		out[sub.AccountID] = sub
	}
	return out, rows.Err()
}
