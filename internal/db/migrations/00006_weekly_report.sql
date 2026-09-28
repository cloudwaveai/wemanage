-- +goose Up
-- The AI report is now weekly and goes only to the employee, who can turn it
-- off. daily_reports keeps its name; each row's date is the week's Monday.
ALTER TABLE employees ADD COLUMN weekly_report_off BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE employees DROP COLUMN weekly_report_off;
