-- +goose Up
-- Preserve the existing personal key and allow separately managed service keys.
ALTER TABLE api_keys ADD COLUMN is_service_key BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE api_keys ADD COLUMN label TEXT NOT NULL DEFAULT '';
DROP INDEX api_keys_one_active;
CREATE UNIQUE INDEX api_keys_one_active ON api_keys(employee_id) WHERE revoked_at IS NULL AND NOT is_service_key;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration';
END $$;
-- +goose StatementEnd
