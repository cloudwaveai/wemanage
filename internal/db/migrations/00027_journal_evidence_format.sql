-- +goose Up
ALTER TABLE wm_full_journal_runs ADD COLUMN plan_format TEXT NOT NULL DEFAULT 'legacy' CHECK (plan_format IN ('legacy','session-evidence-v1'));
-- Immutable copy of the original gateway's per-person daily report. It is a
-- navigation aid, not a substitute for the full source evidence.
CREATE TABLE wm_full_journal_daily_context (
 run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
 employee_id BIGINT NOT NULL REFERENCES employees(id),
 employee_name TEXT NOT NULL,
 summary_md TEXT NOT NULL,
 digest_model TEXT NOT NULL,
 digest_generated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY (run_id,employee_id)
);
-- +goose Down
DROP TABLE wm_full_journal_daily_context;
ALTER TABLE wm_full_journal_runs DROP COLUMN plan_format;
