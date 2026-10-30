package store

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
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

// blobKey is the object-store key for a content hash.
func blobKey(hash [32]byte) string { return "blobs/" + hex.EncodeToString(hash[:]) }

// PutBlobs upserts blobs (dedup by hash) and bumps last_referenced_at. With an
// external backend, the compressed bytes go to the backend and data_zstd stays
// NULL. Round trips are kept constant (not per blob) because the database may
// be across a high-latency link.
func (s *Store) PutBlobs(ctx context.Context, blobs []Blob, refAt time.Time) error {
	if len(blobs) == 0 {
		return nil
	}
	if s.blobBackend == nil {
		return s.putBlobsInline(ctx, blobs, refAt)
	}
	return s.putBlobsExternal(ctx, blobs, refAt)
}

// uploadConcurrency bounds parallel object-store uploads per PutBlobs call.
const uploadConcurrency = 8

// putBlobsExternal: (1) one query finds which hashes already have a row;
// (2) the missing bytes are uploaded in parallel; (3) one batch upserts every
// row. A row is only ever written after its bytes are stored, and keys are
// content hashes, so concurrent writers uploading the same blob are harmless.
// If any upload fails nothing is written and the caller retries.
func (s *Store) putBlobsExternal(ctx context.Context, blobs []Blob, refAt time.Time) error {
	uniq := make([]Blob, 0, len(blobs))
	seen := make(map[[32]byte]bool, len(blobs))
	hashes := make([][]byte, 0, len(blobs))
	for _, b := range blobs {
		if seen[b.Hash] {
			continue
		}
		seen[b.Hash] = true
		uniq = append(uniq, b)
		h := b.Hash
		hashes = append(hashes, h[:])
	}

	existing := make(map[[32]byte]bool, len(uniq))
	rows, err := s.Pool.Query(ctx, `SELECT hash FROM blobs WHERE hash = ANY($1)`, hashes)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		var k [32]byte
		copy(k[:], h)
		existing[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var missing []Blob
	for _, b := range uniq {
		if !existing[b.Hash] {
			missing = append(missing, b)
		}
	}
	if err := s.UploadBlobs(ctx, missing); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for _, b := range uniq {
		batch.Queue(`
			INSERT INTO blobs (hash, kind, data_zstd, raw_size, last_referenced_at)
			VALUES ($1,$2,NULL,$3,$4)
			ON CONFLICT (hash) DO UPDATE SET last_referenced_at = GREATEST(blobs.last_referenced_at, EXCLUDED.last_referenced_at)`,
			b.Hash[:], b.Kind, len(b.Data), refAt)
	}
	br := s.Pool.SendBatch(ctx, batch)
	for range uniq {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return err
		}
	}
	return br.Close()
}

// ExternalBlobs reports whether blob bytes live in an external backend.
func (s *Store) ExternalBlobs() bool { return s.blobBackend != nil }

// UploadBlobs stores blob bytes in the external backend, in parallel. Keys
// are content hashes, so uploading a blob twice is harmless. No-op without a
// backend.
func (s *Store) UploadBlobs(ctx context.Context, blobs []Blob) error {
	if s.blobBackend == nil || len(blobs) == 0 {
		return nil
	}
	var (
		wg      sync.WaitGroup
		errMu   sync.Mutex
		firstEr error
		sem     = make(chan struct{}, uploadConcurrency)
	)
	for _, b := range blobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(b Blob) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.blobBackend.Put(ctx, blobKey(b.Hash), zstdEnc.EncodeAll(b.Data, nil)); err != nil {
				errMu.Lock()
				if firstEr == nil {
					firstEr = err
				}
				errMu.Unlock()
			}
		}(b)
	}
	wg.Wait()
	return firstEr
}

func (s *Store) putBlobsInline(ctx context.Context, blobs []Blob, refAt time.Time) error {
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

// GetBlob returns the kind and decompressed data for a hash. Bytes come from the
// blobs row when stored inline, otherwise from the external backend.
func (s *Store) GetBlob(ctx context.Context, hash [32]byte) (kind string, raw []byte, err error) {
	var comp []byte
	err = s.Pool.QueryRow(ctx, `SELECT kind, data_zstd FROM blobs WHERE hash=$1`, hash[:]).Scan(&kind, &comp)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	if len(comp) == 0 { // external backend
		if s.blobBackend == nil {
			return "", nil, ErrNotFound
		}
		comp, err = s.blobBackend.Get(ctx, blobKey(hash))
		if err != nil {
			return "", nil, err
		}
	}
	raw, err = zstdDec.DecodeAll(comp, nil)
	return kind, raw, err
}

// GetBlobs retrieves a record's unique segments in one database round trip.
// Missing or corrupt entries fail the whole read; callers must not silently
// treat a partial result as a complete record.
func (s *Store) GetBlobs(ctx context.Context, hashes [][32]byte) (map[[32]byte]Blob, error) {
	wanted := make(map[[32]byte]bool, len(hashes))
	flat := make([][]byte, 0, len(hashes))
	for _, h := range hashes {
		if !wanted[h] {
			wanted[h] = true
			flat = append(flat, h[:])
		}
	}
	out := make(map[[32]byte]Blob, len(wanted))
	if len(flat) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT hash,kind,data_zstd FROM blobs WHERE hash=ANY($1)`, flat)
	if err != nil {
		return nil, err
	}
	var external [][32]byte
	for rows.Next() {
		var h, comp []byte
		var kind string
		if err = rows.Scan(&h, &kind, &comp); err != nil {
			rows.Close()
			return nil, err
		}
		if len(h) != 32 {
			rows.Close()
			return nil, errors.New("invalid blob hash")
		}
		var key [32]byte
		copy(key[:], h)
		if len(comp) == 0 {
			external = append(external, key)
			continue
		}
		data, e := zstdDec.DecodeAll(comp, nil)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out[key] = Blob{Hash: key, Kind: kind, Data: data}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, h := range external {
		kind, data, e := s.GetBlob(ctx, h)
		if e != nil {
			return nil, e
		}
		out[h] = Blob{Hash: h, Kind: kind, Data: data}
	}
	if len(out) != len(wanted) {
		return nil, ErrNotFound
	}
	return out, nil
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

// PurgeExpiredBlobs deletes blobs not referenced since before. With an external
// backend, the objects are removed from the bucket first, then the rows.
func (s *Store) PurgeExpiredBlobs(ctx context.Context, before time.Time) (int64, error) {
	if s.blobBackend != nil {
		rows, err := s.Pool.Query(ctx,
			`SELECT hash FROM blobs WHERE last_referenced_at < $1 AND data_zstd IS NULL`, before)
		if err != nil {
			return 0, err
		}
		var keys []string
		for rows.Next() {
			var h []byte
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return 0, err
			}
			var hh [32]byte
			copy(hh[:], h)
			keys = append(keys, blobKey(hh))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		if len(keys) > 0 {
			if err := s.blobBackend.Delete(ctx, keys); err != nil {
				return 0, err
			}
		}
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM blobs WHERE last_referenced_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
