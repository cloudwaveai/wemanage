-- +goose Up
CREATE TABLE work_reviews (
 id BIGSERIAL PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('monthly','quarterly')),
 period_start DATE NOT NULL,
 period_end DATE NOT NULL CHECK (period_end >= period_start),
 content TEXT NOT NULL,
 created_by BIGINT NOT NULL REFERENCES employees(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Rollback requires a reviewed data-preserving migration'; END $$;
-- +goose StatementEnd
