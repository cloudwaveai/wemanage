-- +goose Up
-- Which CPA login an onboarding started (codex, kimi, antigravity, xai, ...).
ALTER TABLE account_onboardings ADD COLUMN provider TEXT NOT NULL DEFAULT 'codex';

-- +goose Down
ALTER TABLE account_onboardings DROP COLUMN provider;
