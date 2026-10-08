-- +goose Up
-- A ChatGPT account's subscription as ChatGPT reports it (accounts/check):
-- when it renews or ends, so admins see the next charge before it happens.
CREATE TABLE account_subscriptions (
  account_id BIGINT PRIMARY KEY REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  plan TEXT NOT NULL DEFAULT '',
  active BOOLEAN NOT NULL DEFAULT false,
  renews_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  cancels_at TIMESTAMPTZ,
  delinquent BOOLEAN NOT NULL DEFAULT false,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  error TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE account_subscriptions;
