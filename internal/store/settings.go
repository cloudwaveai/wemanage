package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// GetSettingRaw returns the raw JSON value for a settings key.
func (s *Store) GetSettingRaw(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var raw json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// PutSettingRaw upserts a settings key.
func (s *Store) PutSettingRaw(ctx context.Context, key string, value json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, []byte(value))
	return err
}

// cardCallbackKey records that Feishu has verified the gateway's card
// callback URL, i.e. that buttons on the gateway's cards will reach it.
const cardCallbackKey = "feishu_card_callback"

// CardCallbackReady reports whether Feishu has verified the card callback URL.
func (s *Store) CardCallbackReady(ctx context.Context) bool {
	_, ok, err := s.GetSettingRaw(ctx, cardCallbackKey)
	return err == nil && ok
}

// MarkCardCallbackReady records Feishu's verification of the card callback URL.
func (s *Store) MarkCardCallbackReady(ctx context.Context) error {
	return s.PutSettingRaw(ctx, cardCallbackKey, json.RawMessage(`{"verified":true}`))
}
