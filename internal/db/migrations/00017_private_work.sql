-- +goose Up
-- Privacy is attached to employee IDs and survives a later name change.
ALTER TABLE employees ADD COLUMN private_work_since TIMESTAMPTZ;
UPDATE employees SET private_work_since=now() WHERE name='林伊默' AND private_work_since IS NULL;
ALTER TABLE work_digest_days ADD COLUMN privacy_safe BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementBegin
CREATE FUNCTION protect_named_employee() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.name = '林伊默' AND NEW.private_work_since IS NULL THEN
    NEW.private_work_since := now();
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER protect_named_employee_before BEFORE INSERT OR UPDATE ON employees
FOR EACH ROW EXECUTE FUNCTION protect_named_employee();
-- +goose StatementBegin
CREATE FUNCTION invalidate_shared_digests() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.private_work_since IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.private_work_since IS NULL) THEN
    UPDATE work_digest_days SET privacy_safe=false;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER invalidate_shared_digests_after AFTER INSERT OR UPDATE ON employees
FOR EACH ROW EXECUTE FUNCTION invalidate_shared_digests();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration';
END $$;
-- +goose StatementEnd
