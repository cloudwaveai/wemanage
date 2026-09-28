-- +goose Up
-- Which upstream an account belongs to (CPA auth-file type): codex (ChatGPT)
-- or another provider CPA supports, e.g. kimi (Kimi Code), used for trials.
ALTER TABLE upstream_accounts ADD COLUMN provider TEXT NOT NULL DEFAULT 'codex';

-- +goose Down
ALTER TABLE upstream_accounts DROP COLUMN provider;
