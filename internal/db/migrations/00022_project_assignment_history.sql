-- +goose Up
CREATE TABLE request_project_history (
 id BIGSERIAL PRIMARY KEY,
 request_id UUID NOT NULL REFERENCES requests(id),
 project_id BIGINT NOT NULL REFERENCES projects(id),
 assigned_by BIGINT REFERENCES employees(id),
 assigned_at TIMESTAMPTZ NOT NULL,
 replaced_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementBegin
CREATE FUNCTION preserve_project_assignment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO request_project_history(request_id,project_id,assigned_by,assigned_at)
 VALUES(OLD.request_id,OLD.project_id,OLD.assigned_by,OLD.assigned_at);
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preserve_project_assignment_before BEFORE UPDATE ON request_projects
FOR EACH ROW EXECUTE FUNCTION preserve_project_assignment();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration'; END $$;
-- +goose StatementEnd
