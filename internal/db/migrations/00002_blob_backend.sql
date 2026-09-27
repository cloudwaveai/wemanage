-- +goose Up
-- Allow the blob bytes to live in an external object store (S3/R2). When an
-- external backend is used, data_zstd is NULL and the compressed bytes are kept
-- in the bucket under key "blobs/<hex hash>"; the row still tracks the hash,
-- kind, size and last_referenced_at for dedup, search-free GC and retention.
ALTER TABLE blobs ALTER COLUMN data_zstd DROP NOT NULL;

-- +goose Down
-- Best effort: only valid if no external-backed (NULL) rows exist.
ALTER TABLE blobs ALTER COLUMN data_zstd SET NOT NULL;
