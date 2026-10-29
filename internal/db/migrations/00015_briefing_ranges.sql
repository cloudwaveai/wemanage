-- +goose Up
-- Preserve existing weekly briefings while allowing independent date ranges.
ALTER TABLE admin_briefings ADD COLUMN period_end DATE;
UPDATE admin_briefings SET period_end = week_start + 4;
ALTER TABLE admin_briefings ALTER COLUMN period_end SET NOT NULL;
ALTER TABLE admin_briefings DROP CONSTRAINT admin_briefings_pkey;
ALTER TABLE admin_briefings ADD PRIMARY KEY (employee_id, week_start, period_end);
ALTER TABLE admin_briefings ADD CONSTRAINT admin_briefings_period_check CHECK (period_end > week_start);

-- +goose Down
-- Keep the most recently generated range when several share a start date.
DELETE FROM admin_briefings a USING admin_briefings b
WHERE a.employee_id = b.employee_id AND a.week_start = b.week_start
  AND (a.generated_at IS NOT NULL, COALESCE(a.generated_at, a.created_at), a.period_end)
    < (b.generated_at IS NOT NULL, COALESCE(b.generated_at, b.created_at), b.period_end);
ALTER TABLE admin_briefings DROP CONSTRAINT admin_briefings_period_check;
ALTER TABLE admin_briefings DROP CONSTRAINT admin_briefings_pkey;
ALTER TABLE admin_briefings ADD PRIMARY KEY (employee_id, week_start);
ALTER TABLE admin_briefings DROP COLUMN period_end;
