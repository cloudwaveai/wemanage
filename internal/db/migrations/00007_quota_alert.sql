-- +goose Up
-- Opt-in reminder when the employee's account quota reaches 80%. The
-- *_alerted columns hold the reset time of the window already reminded
-- about, so each window is mentioned once.
ALTER TABLE employees
  ADD COLUMN quota_alert BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN quota_alerted_primary TIMESTAMPTZ,
  ADD COLUMN quota_alerted_secondary TIMESTAMPTZ;

-- +goose Down
ALTER TABLE employees
  DROP COLUMN quota_alert,
  DROP COLUMN quota_alerted_primary,
  DROP COLUMN quota_alerted_secondary;
