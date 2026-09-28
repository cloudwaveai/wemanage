-- +goose Up
-- Employees allowed to use their key outside the company networks when
-- "office only" is switched on in settings.
ALTER TABLE employees ADD COLUMN remote_allowed BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE employees DROP COLUMN remote_allowed;
