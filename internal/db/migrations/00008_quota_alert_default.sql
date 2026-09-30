-- +goose Up
-- The usage reminder is on unless the employee turns it off, and it also says
-- when a window is used up. The *_exhausted columns hold the reset time of
-- the window already reported as used up, so each is reported once.
ALTER TABLE employees
  ALTER COLUMN quota_alert SET DEFAULT true,
  ADD COLUMN quota_exhausted_primary TIMESTAMPTZ,
  ADD COLUMN quota_exhausted_secondary TIMESTAMPTZ;
UPDATE employees SET quota_alert = true;

-- +goose Down
ALTER TABLE employees
  ALTER COLUMN quota_alert SET DEFAULT false,
  DROP COLUMN quota_exhausted_primary,
  DROP COLUMN quota_exhausted_secondary;
