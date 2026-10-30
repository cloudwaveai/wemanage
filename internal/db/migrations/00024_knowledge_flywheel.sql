-- +goose Up
CREATE TABLE wm_knowledge_schedule (
 id SMALLINT PRIMARY KEY CHECK (id=1),
 enabled BOOLEAN NOT NULL DEFAULT false,
 activated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO wm_knowledge_schedule(id) VALUES(1);
CREATE TABLE wm_knowledge_runs (
 id BIGSERIAL PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('daily','weekly')),
 period_start DATE NOT NULL,
 period_end DATE NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 source_fingerprint TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('running','done','failed','empty')),
 model TEXT NOT NULL DEFAULT '',
 raw_output TEXT NOT NULL DEFAULT '',
 input_tokens BIGINT,
 output_tokens BIGINT,
 error TEXT NOT NULL DEFAULT '',
 warning TEXT NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 finished_at TIMESTAMPTZ,
 UNIQUE(kind,period_start,period_end,version)
);
CREATE TABLE wm_knowledge_sources (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_knowledge_runs(id),
 ordinal INT NOT NULL,
 source_day DATE NOT NULL,
 employee_id BIGINT NOT NULL REFERENCES employees(id),
 employee_name TEXT NOT NULL,
 summary_md TEXT NOT NULL,
 digest_model TEXT NOT NULL,
 digest_generated_at TIMESTAMPTZ NOT NULL,
 UNIQUE(run_id,ordinal)
);
CREATE TABLE wm_knowledge_items (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_knowledge_runs(id),
 category TEXT NOT NULL CHECK(category IN ('project','problem','practice','skill')),
 title TEXT NOT NULL,
 content TEXT NOT NULL,
 project_hint TEXT NOT NULL DEFAULT '',
 evidence JSONB NOT NULL DEFAULT '[]',
 evidence_verified BOOLEAN NOT NULL DEFAULT false,
 status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','confirmed','rejected')),
 reviewed_title TEXT,
 reviewed_content TEXT,
 reviewed_by BIGINT REFERENCES employees(id),
 reviewed_at TIMESTAMPTZ
);
CREATE TABLE wm_knowledge_attempts (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_knowledge_runs(id),
 model TEXT NOT NULL DEFAULT '',
 raw_output TEXT NOT NULL DEFAULT '',
 input_tokens BIGINT,
 output_tokens BIGINT,
 error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wm_knowledge_sources_run ON wm_knowledge_sources(run_id);
CREATE INDEX wm_knowledge_items_run ON wm_knowledge_items(run_id);
-- +goose Down
DROP TABLE wm_knowledge_attempts;
DROP TABLE wm_knowledge_items;
DROP TABLE wm_knowledge_sources;
DROP TABLE wm_knowledge_runs;
DROP TABLE wm_knowledge_schedule;
