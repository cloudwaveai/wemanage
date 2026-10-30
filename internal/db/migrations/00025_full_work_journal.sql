-- +goose Up
-- Independent from the older digest-index flywheel. No model task is enabled
-- by this migration; workers must be configured explicitly after validation.
CREATE TABLE wm_full_journal_settings (
 id SMALLINT PRIMARY KEY CHECK(id=1),
 enabled BOOLEAN NOT NULL DEFAULT false,
 activated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 sol_model TEXT NOT NULL DEFAULT 'gpt-6.1-sol',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO wm_full_journal_settings(id) VALUES(1);
CREATE TABLE wm_full_journal_runs (
 id BIGSERIAL PRIMARY KEY,
 day DATE NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 previous_run_id BIGINT REFERENCES wm_full_journal_runs(id),
 state TEXT NOT NULL CHECK (state IN ('preparing','queued','running','failed','partial','done')),
 source_count INTEGER NOT NULL DEFAULT 0,
 missing_count INTEGER NOT NULL DEFAULT 0,
 planning_complete BOOLEAN NOT NULL DEFAULT false,
 error TEXT NOT NULL DEFAULT '',
 daily_report TEXT NOT NULL DEFAULT '',
 synthesis TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(day, version)
);
CREATE TABLE wm_full_journal_sources (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
 request_id UUID NOT NULL,
 employee_id BIGINT NOT NULL REFERENCES employees(id),
 employee_name TEXT NOT NULL,
 session_id BIGINT,
 started_at TIMESTAMPTZ NOT NULL,
 endpoint TEXT NOT NULL,
 reused_from_source BIGINT REFERENCES wm_full_journal_sources(id),
 reused_request_segments INTEGER NOT NULL DEFAULT 0,
 planned BOOLEAN NOT NULL DEFAULT false,
 status TEXT NOT NULL CHECK (status IN ('pending','ready','missing')),
 error TEXT NOT NULL DEFAULT '',
 UNIQUE(run_id, request_id)
);
CREATE INDEX wm_full_journal_sources_run ON wm_full_journal_sources(run_id,employee_id,session_id,started_at,id);
CREATE TABLE wm_full_journal_blobs (
 run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
 hash BYTEA NOT NULL CHECK (octet_length(hash)=32),
 kind TEXT NOT NULL,
 data BYTEA NOT NULL,
 PRIMARY KEY(run_id,hash)
);
CREATE TABLE wm_full_journal_segments (
 source_id BIGINT NOT NULL REFERENCES wm_full_journal_sources(id),
 direction TEXT NOT NULL CHECK(direction IN ('request','response')),
 ordinal INTEGER NOT NULL,
 hash BYTEA NOT NULL CHECK (octet_length(hash)=32),
 kind TEXT NOT NULL,
 PRIMARY KEY(source_id,direction,ordinal)
);
CREATE TABLE wm_full_journal_batches (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
 phase TEXT NOT NULL CHECK(phase IN ('extract','reduce','daily','synthesis')),
 level INTEGER NOT NULL DEFAULT 0,
 ordinal INTEGER NOT NULL,
 input_manifest JSONB NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed')),
 raw_output TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 input_tokens BIGINT,
 output_tokens BIGINT,
 error TEXT NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(run_id,phase,level,ordinal)
);
CREATE TABLE wm_full_journal_attempts (
 id BIGSERIAL PRIMARY KEY,
 batch_id BIGINT NOT NULL REFERENCES wm_full_journal_batches(id),
 model TEXT NOT NULL DEFAULT '',
 raw_output TEXT NOT NULL DEFAULT '',
 input_tokens BIGINT,
 output_tokens BIGINT,
 error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE wm_full_journal_attempts;
DROP TABLE wm_full_journal_batches;
DROP TABLE wm_full_journal_segments;
DROP TABLE wm_full_journal_blobs;
DROP TABLE wm_full_journal_sources;
DROP TABLE wm_full_journal_runs;
DROP TABLE wm_full_journal_settings;
