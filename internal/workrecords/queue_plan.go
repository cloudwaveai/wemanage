package workrecords

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlanRun is a convenience wrapper. Scheduled workers use PlanNext so that
// a large day can be checkpointed and resumed between bounded cron windows.
func (q *Queue) PlanRun(ctx context.Context, run int64, maxBytes int) error {
	for {
		more, err := q.PlanNext(ctx, run, maxBytes)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// PlanNext plans one source atomically. A crash never publishes only part of
// that source's batches. Earlier source references and completed batches remain
// unchanged and a subsequent invocation continues from the next source.
func (q *Queue) PlanNext(ctx context.Context, run int64, maxBytes int) (bool, error) {
	tx, err := q.Store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var state string
	var pending int
	if err = tx.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND status='pending') FROM wm_full_journal_runs WHERE id=$1 FOR UPDATE`, run).Scan(&state, &pending); err != nil {
		return false, err
	}
	if state != "preparing" || pending != 0 {
		return false, errors.New("source snapshot not ready")
	}
	var private bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&private); err != nil {
		return false, err
	}
	if private {
		return false, errors.New("source privacy changed")
	}
	var id, employee int64
	var name string
	var session *int64
	var at time.Time
	err = tx.QueryRow(ctx, `SELECT id,employee_id,employee_name,session_id,started_at FROM wm_full_journal_sources WHERE run_id=$1 AND status='ready' AND NOT planned ORDER BY employee_id,session_id NULLS LAST,started_at,id LIMIT 1`, run).Scan(&id, &employee, &name, &session, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract'`, run).Scan(&count); err != nil {
			return false, err
		}
		if count == 0 {
			_, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET state='partial',planning_complete=true,error='当日没有可完整读取的记录；这不代表没有工作',updated_at=now() WHERE id=$1`, run)
		} else {
			_, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET state='queued',planning_complete=true,updated_at=now() WHERE id=$1`, run)
		}
		if err != nil {
			return false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var ordinal int
	if err = tx.QueryRow(ctx, `SELECT coalesce(max(ordinal)+1,0) FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract'`, run).Scan(&ordinal); err != nil {
		return false, err
	}
	var tailID int64
	var tailData []byte
	tailErr := tx.QueryRow(ctx, `SELECT id,input_manifest FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract' ORDER BY ordinal DESC LIMIT 1`, run).Scan(&tailID, &tailData)
	if tailErr != nil && !errors.Is(tailErr, pgx.ErrNoRows) {
		return false, tailErr
	}
	planner, err := NewPlanner(maxBytes, func(batch PlannedBatch) error {
		encoded, e := json.Marshal(batch)
		if e != nil {
			return e
		}
		if tailID != 0 {
			_, e = tx.Exec(ctx, `UPDATE wm_full_journal_batches SET input_manifest=$2 WHERE id=$1 AND state='queued'`, tailID, encoded)
			tailID = 0
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',$2,$3,'queued')`, run, ordinal, encoded)
		if e == nil {
			ordinal++
		}
		return e
	})
	if err != nil {
		return false, err
	}
	if tailID != 0 {
		if err = json.Unmarshal(tailData, &planner.current); err != nil {
			return false, err
		}
	}
	// The immediately preceding planned request in this same session supplies
	// only identities, so already-emitted history is not transferred again.
	var previousID int64
	if session != nil {
		var prevID int64
		e := tx.QueryRow(ctx, `SELECT id FROM wm_full_journal_sources WHERE run_id=$1 AND employee_id=$2 AND session_id=$3 AND status='ready' AND planned AND (started_at,id)<($4,$5) ORDER BY started_at DESC,id DESC LIMIT 1`, run, employee, *session, at, id).Scan(&prevID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return false, e
		}
		if e == nil {
			previousID = prevID
			r, e := tx.Query(ctx, `SELECT hash,kind FROM wm_full_journal_segments WHERE source_id=$1 AND direction='request' ORDER BY ordinal`, prevID)
			if e != nil {
				return false, e
			}
			var ids []segmentIdentity
			for r.Next() {
				var h []byte
				var kind string
				if e = r.Scan(&h, &kind); e != nil {
					r.Close()
					return false, e
				}
				if len(h) != 32 {
					r.Close()
					return false, errors.New("invalid snapshot hash")
				}
				var key [32]byte
				copy(key[:], h)
				ids = append(ids, segmentIdentity{hash: key, kind: kind})
			}
			if e = r.Err(); e != nil {
				r.Close()
				return false, e
			}
			r.Close()
			planner.previous[sessionKey{employee, *session}] = prior{source: prevID, request: ids}
		}
	}
	segs, err := tx.Query(ctx, `SELECT s.direction,s.kind,s.hash,b.hash FROM wm_full_journal_segments s LEFT JOIN wm_full_journal_blobs b ON b.run_id=$2 AND b.hash=s.hash WHERE s.source_id=$1 ORDER BY s.direction DESC,s.ordinal`, id, run)
	if err != nil {
		return false, err
	}
	var record Record
	for segs.Next() {
		var dir, kind string
		var hash, present []byte
		if err = segs.Scan(&dir, &kind, &hash, &present); err != nil {
			segs.Close()
			return false, err
		}
		if len(hash) != 32 || len(present) != 32 {
			segs.Close()
			return false, errors.New("missing snapshot hash")
		}
		var key [32]byte
		copy(key[:], hash)
		seg := Segment{Hash: key, Kind: kind}
		if dir == "request" {
			record.Request = append(record.Request, seg)
		} else if dir == "response" {
			record.Response = append(record.Response, seg)
		} else {
			segs.Close()
			return false, errors.New("invalid direction")
		}
	}
	if err = segs.Err(); err != nil {
		segs.Close()
		return false, err
	}
	segs.Close()
	if len(record.Request)+len(record.Response) == 0 {
		return false, errors.New("source has no snapshot segments")
	}
	src := SourceRecord{SourceID: id, EmployeeID: employee, EmployeeName: name, SessionID: session, StartedAt: at, Record: record}
	reuse := planner.SharedInput(src)
	needed := map[[32]byte]bool{}
	args := [][]byte{}
	for i, seg := range append(append([]Segment{}, record.Request...), record.Response...) {
		if i < len(record.Request) && i >= reuse.current && i < reuse.current+reuse.count {
			continue
		}
		if !needed[seg.Hash] {
			needed[seg.Hash] = true
			args = append(args, seg.Hash[:])
		}
	}
	dataByHash := map[[32]byte][]byte{}
	if len(args) > 0 {
		blobs, e := tx.Query(ctx, `SELECT hash,data FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=ANY($2)`, run, args)
		if e != nil {
			return false, e
		}
		for blobs.Next() {
			var h, data []byte
			if e = blobs.Scan(&h, &data); e != nil {
				blobs.Close()
				return false, e
			}
			if len(h) != 32 {
				blobs.Close()
				return false, errors.New("invalid snapshot hash")
			}
			var key [32]byte
			copy(key[:], h)
			if sha256.Sum256(data) != key {
				blobs.Close()
				return false, errors.New("snapshot data mismatch")
			}
			dataByHash[key] = data
		}
		if e = blobs.Err(); e != nil {
			blobs.Close()
			return false, e
		}
		blobs.Close()
		if len(dataByHash) != len(needed) {
			return false, errors.New("snapshot blob missing")
		}
	}
	for i := range src.Record.Request {
		if i >= reuse.current && i < reuse.current+reuse.count {
			continue
		}
		src.Record.Request[i].Data = dataByHash[src.Record.Request[i].Hash]
	}
	for i := range src.Record.Response {
		src.Record.Response[i].Data = dataByHash[src.Record.Response[i].Hash]
	}
	if err = planner.Add(src); err != nil {
		return false, err
	}
	if err = planner.Finish(); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wm_full_journal_sources SET planned=true,reused_request_segments=$2,reused_from_source=$3 WHERE id=$1`, id, reuse.count, func() any {
		if reuse.count > 0 {
			return previousID
		}
		return nil
	}()); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
