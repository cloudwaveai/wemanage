-- +goose Up
CREATE TABLE projects (
 id BIGSERIAL PRIMARY KEY,
 name TEXT NOT NULL,
 category TEXT NOT NULL CHECK (category IN ('client','infrastructure')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE api_keys ADD COLUMN project_id BIGINT REFERENCES projects(id);
CREATE TABLE request_projects (
 request_id UUID PRIMARY KEY REFERENCES requests(id),
 project_id BIGINT NOT NULL REFERENCES projects(id),
 assigned_by BIGINT REFERENCES employees(id),
 assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE api_channel_rates (
 id BIGSERIAL PRIMARY KEY,
 channel_id BIGINT NOT NULL REFERENCES api_channels(id),
 model TEXT NOT NULL,
 currency TEXT NOT NULL CHECK (currency IN ('CNY','USD')),
 input_per_million NUMERIC(24,8) NOT NULL CHECK (input_per_million>=0),
 cached_per_million NUMERIC(24,8) NOT NULL CHECK (cached_per_million>=0),
 output_per_million NUMERIC(24,8) NOT NULL CHECK (output_per_million>=0),
 effective_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(channel_id,model,effective_at)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration'; END $$;
-- +goose StatementEnd
