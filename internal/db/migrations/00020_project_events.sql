-- +goose Up
CREATE TABLE project_events (
 id BIGSERIAL PRIMARY KEY,
 project_id BIGINT NOT NULL REFERENCES projects(id),
 employee_id BIGINT NOT NULL REFERENCES employees(id),
 occurred_at TIMESTAMPTZ NOT NULL,
 content TEXT NOT NULL,
 evidence TEXT NOT NULL DEFAULT '',
 created_by BIGINT REFERENCES employees(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX project_events_timeline ON project_events(project_id,occurred_at DESC,id DESC);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration'; END $$;
-- +goose StatementEnd
