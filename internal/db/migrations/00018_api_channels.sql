-- +goose Up
CREATE TABLE api_channels (
 id BIGSERIAL PRIMARY KEY,
 name TEXT NOT NULL,
 protocol TEXT NOT NULL CHECK (protocol IN ('openai', 'anthropic')),
 base_url TEXT NOT NULL,
 credential_cipher BYTEA NOT NULL,
 active BOOLEAN NOT NULL DEFAULT true,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE api_keys ADD COLUMN api_channel_id BIGINT REFERENCES api_channels(id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration';
END $$;
-- +goose StatementEnd
