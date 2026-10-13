-- +goose Up
-- What each employee worked on in a day, summarized by an LLM from that
-- day's conversation text, for admins (the work board).
CREATE TABLE work_digests (
  day DATE NOT NULL,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  summary_md TEXT NOT NULL,
  sessions INT NOT NULL DEFAULT 0,
  messages INT NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (day, employee_id)
);

-- One row per summarized day: the team overview and how the run went.
CREATE TABLE work_digest_days (
  day DATE PRIMARY KEY,
  overview_md TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','done','failed')),
  error TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ
);

-- +goose Down
DROP TABLE work_digest_days;
DROP TABLE work_digests;
