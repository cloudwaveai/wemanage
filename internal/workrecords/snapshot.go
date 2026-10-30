package workrecords

import (
	"context"
	"crypto/sha256"
	"fmt"

	"aiseat/internal/store"
)

type scopedReader struct {
	queue   *Queue
	owner   int64
	session *int64
}

func (s scopedReader) GetRequestEnc(ctx context.Context, id [16]byte) (store.RequestEnc, error) {
	e, err := s.queue.ownedEncoding(ctx, id)
	if err != nil {
		return store.RequestEnc{}, err
	}
	sameSession := (e.session == nil && s.session == nil) || (e.session != nil && s.session != nil && *e.session == *s.session)
	if e.owner != s.owner || !sameSession {
		return store.RequestEnc{}, fmt.Errorf("%w: prefix belongs to another identity or session", ErrInvalidRecord)
	}
	return e.enc, nil
}
func (s scopedReader) GetBlob(ctx context.Context, h [32]byte) (string, []byte, error) {
	return s.queue.Store.GetBlob(ctx, h)
}

// snapshotRecord transfers each distinct blob at most once per journal run.
// Reused entries retain their kind and hash without downloading the contents
// again; snapshot planning verifies their stored bytes before model use.
func (q *Queue) snapshotRecord(ctx context.Context, run int64, row store.RequestRow) (Record, error) {
	if row.BodiesPurged {
		return Record{}, fmt.Errorf("%w: purged", ErrInvalidRecord)
	}
	req, err := requestHashes(ctx, scopedReader{queue: q, owner: row.EmployeeID, session: row.SessionID}, row.ID)
	if err != nil {
		return Record{}, err
	}
	resp, err := hashes(row.RespHashes)
	if err != nil {
		return Record{}, err
	}
	if len(req)+len(resp) == 0 {
		return Record{}, fmt.Errorf("%w: no segments", ErrInvalidRecord)
	}
	seen := map[[32]byte]bool{}
	all := make([][32]byte, 0, len(req)+len(resp))
	args := make([][]byte, 0, len(req)+len(resp))
	for _, h := range append(append([][32]byte{}, req...), resp...) {
		if !seen[h] {
			seen[h] = true
			all = append(all, h)
			args = append(args, h[:])
		}
	}
	existing := map[[32]byte]string{}
	rows, err := q.Store.Pool.Query(ctx, `SELECT hash,kind FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=ANY($2)`, run, args)
	if err != nil {
		return Record{}, err
	}
	for rows.Next() {
		var h []byte
		var kind string
		if err = rows.Scan(&h, &kind); err != nil {
			rows.Close()
			return Record{}, err
		}
		if len(h) != 32 {
			rows.Close()
			return Record{}, fmt.Errorf("%w: invalid snapshot hash", ErrInvalidRecord)
		}
		var key [32]byte
		copy(key[:], h)
		existing[key] = kind
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Record{}, err
	}
	rows.Close()
	var missing [][32]byte
	for _, h := range all {
		if _, ok := existing[h]; !ok {
			missing = append(missing, h)
		}
	}
	blobs, err := q.Store.GetBlobs(ctx, missing)
	if err != nil {
		return Record{}, err
	}
	load := func(list [][32]byte) ([]Segment, error) {
		out := make([]Segment, 0, len(list))
		for _, h := range list {
			if kind, ok := existing[h]; ok {
				out = append(out, Segment{Hash: h, Kind: kind})
				continue
			}
			b, ok := blobs[h]
			if !ok {
				return nil, fmt.Errorf("%w: missing segment", ErrInvalidRecord)
			}
			if sha256.Sum256(b.Data) != h {
				return nil, fmt.Errorf("%w: hash mismatch", ErrInvalidRecord)
			}
			out = append(out, Segment{Hash: h, Kind: b.Kind, Data: b.Data})
		}
		return out, nil
	}
	a, err := load(req)
	if err != nil {
		return Record{}, err
	}
	b, err := load(resp)
	if err != nil {
		return Record{}, err
	}
	return Record{Request: a, Response: b}, nil
}
