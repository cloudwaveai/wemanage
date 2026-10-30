-- +goose Up
ALTER TABLE wm_full_journal_runs ADD COLUMN superseded BOOLEAN NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE wm_full_journal_runs DROP COLUMN superseded;
