package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// BlobRef is a blob hash referenced at a time (bumps last_referenced_at).
type BlobRef struct {
	Hash [32]byte
	At   time.Time
}

// SessionAdvance is a session's state after its latest request in a batch.
type SessionAdvance struct {
	SessionID     int64
	LastRequestID [16]byte
	FullHashes    []byte
	ChainDepth    int
	InputCount    int
	LastAt        time.Time
	Requests      int // requests of this session in the batch
}

// LogBatch is everything the log recorder writes for a group of requests.
type LogBatch struct {
	NewBlobs []Blob // may not exist yet: inserted (bytes inline, or NULL when external)
	NewAt    []time.Time
	Bumps    []BlobRef // already stored: only last_referenced_at moves forward
	Requests []RequestRow
	Texts    []TextRow
	Sessions []SessionAdvance
}

// CommitLogBatch writes a LogBatch in ONE round trip. pgx sends the batch as
// a single pipeline ending in one Sync, so it runs as one implicit
// transaction: all of it lands or none does. External blob bytes must have
// been uploaded (UploadBlobs) before calling this.
func (s *Store) CommitLogBatch(ctx context.Context, lb LogBatch) error {
	batch := &pgx.Batch{}

	if len(lb.NewBlobs) > 0 {
		// De-duplicate: one INSERT ... ON CONFLICT DO UPDATE cannot touch a row twice.
		idx := map[[32]byte]int{}
		var hashes, datas [][]byte
		var kinds []string
		var sizes []int32
		var ats []time.Time
		for i, b := range lb.NewBlobs {
			at := lb.NewAt[i]
			if j, ok := idx[b.Hash]; ok {
				if at.After(ats[j]) {
					ats[j] = at
				}
				continue
			}
			idx[b.Hash] = len(hashes)
			h := b.Hash
			hashes = append(hashes, h[:])
			kinds = append(kinds, b.Kind)
			sizes = append(sizes, int32(len(b.Data)))
			ats = append(ats, at)
			if s.blobBackend == nil {
				datas = append(datas, zstdEnc.EncodeAll(b.Data, nil))
			} else {
				datas = append(datas, nil)
			}
		}
		batch.Queue(`
			INSERT INTO blobs (hash, kind, data_zstd, raw_size, last_referenced_at)
			SELECT * FROM unnest($1::bytea[], $2::text[], $3::bytea[], $4::int[], $5::timestamptz[])
			ON CONFLICT (hash) DO UPDATE SET last_referenced_at = GREATEST(blobs.last_referenced_at, EXCLUDED.last_referenced_at)`,
			hashes, kinds, datas, sizes, ats)
	}

	if len(lb.Bumps) > 0 {
		latest := map[[32]byte]time.Time{}
		for _, r := range lb.Bumps {
			if t, ok := latest[r.Hash]; !ok || r.At.After(t) {
				latest[r.Hash] = r.At
			}
		}
		hashes := make([][]byte, 0, len(latest))
		ats := make([]time.Time, 0, len(latest))
		for h, t := range latest {
			hh := h
			hashes = append(hashes, hh[:])
			ats = append(ats, t)
		}
		batch.Queue(`
			UPDATE blobs SET last_referenced_at = GREATEST(blobs.last_referenced_at, v.at)
			FROM unnest($1::bytea[], $2::timestamptz[]) AS v(hash, at)
			WHERE blobs.hash = v.hash`, hashes, ats)
	}

	for _, r := range lb.Requests {
		batch.Queue(insertRequestSQL, requestArgs(r)...)
	}
	for _, t := range lb.Texts {
		batch.Queue(insertTextSQL, t.RequestID[:], t.EmployeeID, t.SessionID, t.StartedAt, t.Role, t.Text)
	}
	for _, a := range lb.Sessions {
		batch.Queue(`
			UPDATE sessions SET
				last_request_id=$2, last_req_hashes=$3, last_req_chain_depth=$4,
				last_input_count=$5, last_at=GREATEST(last_at, $6), request_count=request_count+$7
			WHERE id=$1`,
			a.SessionID, a.LastRequestID[:], a.FullHashes, a.ChainDepth, a.InputCount, a.LastAt, a.Requests)
	}
	if batch.Len() == 0 {
		return nil
	}

	br := s.Pool.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return err
		}
	}
	return br.Close()
}
