-- +goose Up
-- Service accounts: callers that are not people, such as the company's New API
-- platform. Administrators create them and issue their keys; they are never
-- synced from Feishu (no open_id), get no weekly report and register no devices.
ALTER TABLE employees ADD COLUMN is_service BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE employees DROP COLUMN is_service;
