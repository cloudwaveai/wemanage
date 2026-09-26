package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"
)

// Blob is a deduplicated content chunk (a request/response segment).
type Blob struct {
	Hash [32]byte
	Kind string
	Data []byte // raw (uncompressed); stored zstd-compressed
}

var (
	zstdEnc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	zstdDec, _ = zstd.NewReader(nil)
)

// PutBlobs upserts blobs (dedup by hash) and bumps last_referenced_at.
func (s *Store) PutBlobs(ctx context.Context, blobs []Blob, refAt time.Time) error {
	if len(blobs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, b := range blobs {
		comp := zstdEnc.EncodeAll(b.Data, nil)
		batch.Queue(`
			INSERT INTO blobs (hash, kind, data_zstd, raw_size, last_referenced_at)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (hash) DO UPDATE SET last_referenced_at = GREATEST(blobs.last_referenced_at, EXCLUDED.last_referenced_at)`,
			b.Hash[:], b.Kind, comp, len(b.Data), refAt)
	}
	br := s.Pool.SendBatch(ctx, batch)
	defer br.Close()
	for range blobs {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// GetBlob returns the kind and decompressed data for a hash.
func (s *Store) GetBlob(ctx context.Context, hash [32]byte) (kind string, raw []byte, err error) {
	var comp []byte
	err = s.Pool.QueryRow(ctx, `SELECT kind, data_zstd FROM blobs WHERE hash=$1`, hash[:]).Scan(&kind, &comp)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	raw, err = zstdDec.DecodeAll(comp, nil)
	return kind, raw, err
}

// TouchBlobs bumps last_referenced_at for a set of hashes.
func (s *Store) TouchBlobs(ctx context.Context, hashes [][32]byte, at time.Time) error {
	if len(hashes) == 0 {
		return nil
	}
	flat := make([][]byte, len(hashes))
	for i := range hashes {
		flat[i] = hashes[i][:]
	}
	_, err := s.Pool.Exec(ctx,
		`UPDATE blobs SET last_referenced_at=GREATEST(last_referenced_at,$2) WHERE hash = ANY($1)`,
		flat, at)
	return err
}

// PurgeExpiredBlobs deletes blobs not referenced since before.
func (s *Store) PurgeExpiredBlobs(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM blobs WHERE last_referenced_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
