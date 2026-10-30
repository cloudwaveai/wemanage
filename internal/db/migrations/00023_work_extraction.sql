-- +goose Up
CREATE TABLE wm_extraction_runs (
 id BIGSERIAL PRIMARY KEY,
 source_request_id UUID NOT NULL REFERENCES requests(id),
 created_by BIGINT NOT NULL REFERENCES employees(id),
 model TEXT NOT NULL,
 raw_output TEXT NOT NULL,
 input_tokens BIGINT,
 output_tokens BIGINT,
 cleaned_log TEXT NOT NULL,
 limitations TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE wm_extracted_items (
 id BIGSERIAL PRIMARY KEY,
 run_id BIGINT NOT NULL REFERENCES wm_extraction_runs(id),
 kind TEXT NOT NULL CHECK (kind IN ('knowledge','skill')),
 title TEXT NOT NULL,
 content TEXT NOT NULL,
 evidence_quote TEXT NOT NULL,
 evidence_verified BOOLEAN NOT NULL,
 status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','confirmed','rejected')),
 reviewed_title TEXT,
 reviewed_content TEXT,
 reviewed_by BIGINT REFERENCES employees(id),
 reviewed_at TIMESTAMPTZ
);
CREATE INDEX wm_extraction_runs_time ON wm_extraction_runs(created_at DESC);
CREATE INDEX wm_extracted_items_run ON wm_extracted_items(run_id);
-- +goose Down
DROP TABLE wm_extracted_items;
DROP TABLE wm_extraction_runs;
