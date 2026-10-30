package workrecords

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"aiseat/internal/store"
	"github.com/jackc/pgx/v5"
)

// Queue snapshots the identity and order of every eligible request for a day.
// Capture and model execution are separate; creating a queue never calls a model.
type Queue struct {
	Store    *store.Store
	mu       sync.Mutex
	encCache map[[16]byte]ownedEncoding
}

type ownedEncoding struct {
	enc     store.RequestEnc
	owner   int64
	session *int64
}

func (q *Queue) ownedEncoding(ctx context.Context, id [16]byte) (ownedEncoding, error) {
	q.mu.Lock()
	e, ok := q.encCache[id]
	q.mu.Unlock()
	if ok {
		return e, nil
	}
	var prefix []byte
	err := q.Store.Pool.QueryRow(ctx, `SELECT employee_id,session_id,req_prefix_of,req_prefix_len,req_chain_depth,req_hashes FROM requests WHERE id=$1`, id[:]).Scan(&e.owner, &e.session, &prefix, &e.enc.PrefixLen, &e.enc.ChainDepth, &e.enc.Hashes)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, store.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if len(prefix) > 0 {
		if len(prefix) != 16 {
			return e, fmt.Errorf("%w: invalid prefix ID", ErrInvalidRecord)
		}
		var p [16]byte
		copy(p[:], prefix)
		e.enc.PrefixOf = &p
	}
	q.mu.Lock()
	if len(q.encCache) >= 32768 {
		q.encCache = nil
	}
	if q.encCache == nil {
		q.encCache = make(map[[16]byte]ownedEncoding)
	}
	q.encCache[id] = e
	q.mu.Unlock()
	return e, nil
}

func (q *Queue) Start(ctx context.Context, day time.Time) (int64, error) {
	if q.Store == nil {
		return 0, errors.New("store not configured")
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	to := from.AddDate(0, 0, 1)
	tx, err := q.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	// Serializes version allocation for all processes without blocking readers.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7419752102)`); err != nil {
		return 0, err
	}
	var run int64
	err = tx.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,version,state,previous_run_id) VALUES($1::date,COALESCE((SELECT max(version)+1 FROM wm_full_journal_runs WHERE day=$1::date),1),'preparing',(SELECT id FROM wm_full_journal_runs WHERE day=$1::date-1 AND state IN ('done','partial') ORDER BY version DESC LIMIT 1)) RETURNING id`, from).Scan(&run)
	if err != nil {
		return 0, err
	}
	// All matching sources are enumerated in one database snapshot. The shared
	// report excludes the boss and any other private employee at creation.
	_, err = tx.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) SELECT $1,r.id,e.id,e.name,r.session_id,r.started_at,r.endpoint,'pending' FROM requests r JOIN employees e ON e.id=r.employee_id WHERE r.started_at >= $2 AND r.started_at < $3 AND e.private_work_since IS NULL AND e.name <> '林伊默' ORDER BY r.started_at,r.id`, run, from, to)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET source_count=(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1) WHERE id=$1`, run)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return run, nil
}

// SnapshotNext persists one complete record, including every occurrence of
// every stored segment, or records a coverage gap. The transaction prevents
// an interrupted copy from being mistaken for a complete record. One caller
// must hold a run-specific worker lock when using this method concurrently.
func (q *Queue) SnapshotNext(ctx context.Context, run int64) (bool, error) {
	var source, owner int64
	var rawID []byte
	err := q.Store.Pool.QueryRow(ctx, `SELECT id,employee_id,request_id FROM wm_full_journal_sources WHERE run_id=$1 AND status='pending' ORDER BY started_at,id LIMIT 1`, run).Scan(&source, &owner, &rawID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(rawID) != 16 {
		return false, errors.New("invalid request ID")
	}
	var id [16]byte
	copy(id[:], rawID)
	row, err := q.Store.GetRequestFull(ctx, id)
	var record Record
	if err == nil {
		record, err = q.snapshotRecord(ctx, run, row)
	}
	if err != nil && !errors.Is(err, ErrInvalidRecord) && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	tx, txErr := q.Store.Pool.Begin(ctx)
	if txErr != nil {
		return false, txErr
	}
	defer tx.Rollback(context.Background())
	var permitted bool
	txErr = tx.QueryRow(ctx, `SELECT private_work_since IS NULL AND name <> '林伊默' FROM employees WHERE id=$1 FOR SHARE`, owner).Scan(&permitted)
	if txErr != nil || !permitted {
		return false, errors.New("source privacy changed; stop run")
	}
	var status string
	if txErr = tx.QueryRow(ctx, `SELECT status FROM wm_full_journal_sources WHERE id=$1 FOR UPDATE`, source).Scan(&status); txErr != nil {
		return false, txErr
	}
	if status != "pending" {
		return true, nil
	}
	if err != nil {
		// The error category, not raw user content, is retained in the manifest.
		_, txErr = tx.Exec(ctx, `UPDATE wm_full_journal_sources SET status='missing',error=$2 WHERE id=$1`, source, "正文未能完整重建")
		if txErr == nil {
			_, txErr = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET missing_count=missing_count+1,updated_at=now() WHERE id=$1`, run)
		}
	} else {
		all := append(append([]Segment{}, record.Request...), record.Response...)
		unique := map[[32]byte]bool{}
		hashes := make([][]byte, 0, len(all))
		for _, seg := range all {
			if !unique[seg.Hash] {
				unique[seg.Hash] = true
				hashes = append(hashes, seg.Hash[:])
			}
		}
		existing := map[[32]byte]bool{}
		rows, e := tx.Query(ctx, `SELECT hash FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=ANY($2)`, run, hashes)
		if e != nil {
			return false, e
		}
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				rows.Close()
				return false, e
			}
			var h [32]byte
			copy(h[:], b)
			existing[h] = true
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return false, e
		}
		rows.Close()
		batch := &pgx.Batch{}
		operations := 0
		for _, group := range []struct {
			direction string
			segments  []Segment
		}{{"request", record.Request}, {"response", record.Response}} {
			for ordinal, seg := range group.segments {
				if !existing[seg.Hash] {
					batch.Queue(`INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,$3,$4) ON CONFLICT (run_id,hash) DO NOTHING`, run, seg.Hash[:], seg.Kind, seg.Data)
					operations++
					existing[seg.Hash] = true
				}
				batch.Queue(`INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,$2,$3,$4,$5)`, source, group.direction, ordinal, seg.Hash[:], seg.Kind)
				operations++
			}
		}
		br := tx.SendBatch(ctx, batch)
		for i := 0; i < operations; i++ {
			if _, e = br.Exec(); e != nil {
				br.Close()
				return false, fmt.Errorf("snapshot segment: %w", e)
			}
		}
		if e = br.Close(); e != nil {
			return false, e
		}
		_, txErr = tx.Exec(ctx, `UPDATE wm_full_journal_sources SET status='ready' WHERE id=$1`, source)
	}
	if txErr != nil {
		return false, txErr
	}
	if txErr = tx.Commit(ctx); txErr != nil {
		return false, txErr
	}
	return true, nil
}
