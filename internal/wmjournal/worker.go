package wmjournal

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"aiseat/internal/workrecords"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProcessOne executes at most one extraction. It requires the old scheduler to
// remain disabled and leaves unknown outcomes as running for human review.
func ProcessOne(ctx context.Context, pool *pgxpool.Pool, run int64, model Extractor) (bool, error) {
	if run <= 0 || model == nil {
		return false, errors.New("invalid worker configuration")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil || !locked {
		return false, errors.New("scheduler unavailable")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	var enabled bool
	if err = conn.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled); err != nil || enabled {
		return false, errors.New("scheduler must remain paused")
	}
	return processOne(ctx, pool, run, model, false)
}

// ProcessPair advances up to two distinct batches. It owns the scheduler lock
// for both calls, and refuses to start if any previous call is unresolved.
func ProcessPair(ctx context.Context, pool *pgxpool.Pool, run int64, model Extractor) (bool, error) {
	if run <= 0 || model == nil {
		return false, errors.New("invalid worker configuration")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked, enabled, blocked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil || !locked {
		return false, errors.New("scheduler unavailable")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	if err = conn.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled); err != nil || enabled {
		return false, errors.New("scheduler must remain paused")
	}
	if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_batches WHERE run_id=$1 AND state IN ('running','failed'))`, run).Scan(&blocked); err != nil {
		return false, err
	}
	if blocked {
		return false, errors.New("batch failed or outcome unknown")
	}
	type result struct {
		worked bool
		err    error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i].worked, results[i].err = processOne(ctx, pool, run, model, true)
		}(i)
	}
	wg.Wait()
	worked := false
	for _, r := range results {
		worked = worked || r.worked
		if r.err != nil {
			return worked, r.err
		}
	}
	return worked, nil
}

func processOne(ctx context.Context, pool *pgxpool.Pool, run int64, model Extractor, claimEarly bool) (worked bool, retErr error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var enabled bool
	var id int64
	var raw []byte
	if claimEarly {
		err = conn.QueryRow(ctx, `WITH next AS (SELECT b.id FROM wm_full_journal_batches b JOIN wm_full_journal_runs r ON r.id=b.run_id WHERE b.run_id=$1 AND b.phase='extract' AND b.level=0 AND b.state='queued' AND NOT r.superseded AND r.state IN ('queued','running') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches x WHERE x.run_id=r.id AND x.state='failed') ORDER BY b.ordinal FOR UPDATE OF b SKIP LOCKED LIMIT 1) UPDATE wm_full_journal_batches b SET state='running',started_at=now(),updated_at=now() FROM next WHERE b.id=next.id RETURNING b.id,b.input_manifest`, run).Scan(&id, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
	} else {
		err = conn.QueryRow(ctx, `SELECT b.id,b.input_manifest FROM wm_full_journal_batches b JOIN wm_full_journal_runs r ON r.id=b.run_id WHERE b.run_id=$1 AND b.phase='extract' AND b.level=0 AND b.state='queued' AND NOT r.superseded AND r.state IN ('queued','running') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches x WHERE x.run_id=r.id AND x.state IN ('running','failed')) ORDER BY b.ordinal LIMIT 1`, run).Scan(&id, &raw)
	}
	if err != nil {
		return false, err
	}
	modelStarted := false
	defer func() {
		if claimEarly && retErr != nil && !modelStarted {
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = pool.Exec(c, `UPDATE wm_full_journal_batches SET state='failed',error='调用前检查未通过，原始记录保留',updated_at=now() WHERE id=$1 AND state='running'`, id)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1 FOR SHARE`).Scan(&enabled); err != nil || enabled {
		return false, errors.New("scheduler must remain paused")
	}
	// Hold only the identities represented in this batch. Locking every person
	// in the entire day needlessly blocks unrelated account synchronization for
	// the duration of a remote model call.
	rows, err := tx.Query(ctx, `SELECT e.private_work_since IS NULL AND e.name <> '林伊默' FROM employees e WHERE e.id IN (SELECT (p->>'employee_id')::bigint FROM jsonb_array_elements($1::jsonb->'parts') p) ORDER BY e.id FOR SHARE`, raw)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var allowed bool
		if rows.Scan(&allowed) != nil || !allowed {
			rows.Close()
			return false, errors.New("source privacy changed")
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return false, errors.New("privacy check failed")
	}
	rows.Close()
	var allVisible bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&allVisible); err != nil || !allVisible {
		return false, errors.New("source privacy changed")
	}
	var valid bool
	err = tx.QueryRow(ctx, `WITH refs AS (SELECT (p->>'employee_id')::bigint employee_id,p->>'employee_name' employee_name,(p->>'session_id')::bigint session_id,p->>'direction' direction,p->>'kind' kind,decode(p->>'hash','hex') hash,(o->>'source_id')::bigint source_id,(o->>'ordinal')::int ordinal,(o->>'time')::timestamptz started_at FROM jsonb_array_elements($2::jsonb->'parts') p CROSS JOIN LATERAL jsonb_array_elements(p->'occurrences') o) SELECT NOT EXISTS(SELECT 1 FROM refs x LEFT JOIN wm_full_journal_sources s ON s.id=x.source_id AND s.run_id=$1 LEFT JOIN wm_full_journal_segments g ON g.source_id=s.id AND g.direction=x.direction AND g.ordinal=x.ordinal WHERE s.id IS NULL OR s.employee_id IS DISTINCT FROM x.employee_id OR s.employee_name IS DISTINCT FROM COALESCE(x.employee_name,'') OR s.session_id IS DISTINCT FROM x.session_id OR s.started_at IS DISTINCT FROM x.started_at OR g.hash IS DISTINCT FROM x.hash OR g.kind IS DISTINCT FROM x.kind)`, run, raw).Scan(&valid)
	if err != nil || !valid {
		return false, errors.New("source validation failed")
	}
	var batch workrecords.EvidenceBatch
	if json.Unmarshal(raw, &batch) != nil {
		return false, errors.New("invalid manifest")
	}
	seen := map[string]bool{}
	var hashes [][]byte
	for _, part := range batch.Parts {
		if seen[part.Hash] {
			continue
		}
		hash, e := hex.DecodeString(part.Hash)
		if e != nil || len(hash) != 32 {
			return false, errors.New("invalid hash")
		}
		seen[part.Hash] = true
		hashes = append(hashes, hash)
	}
	rows, err = tx.Query(ctx, `SELECT hash,data FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=ANY($2)`, run, hashes)
	if err != nil {
		return false, err
	}
	blobs := map[string][]byte{}
	for rows.Next() {
		var h, d []byte
		if rows.Scan(&h, &d) != nil {
			rows.Close()
			return false, errors.New("source read failed")
		}
		blobs[hex.EncodeToString(h)] = d
	}
	if rows.Err() != nil {
		rows.Close()
		return false, errors.New("source read failed")
	}
	rows.Close()
	material, gap, err := Materialize(batch, blobs, 100000)
	if err != nil {
		return false, err
	}
	opaqueNote, opaqueOnly, err := OpaqueOnly(batch, blobs)
	if err != nil {
		return false, err
	}
	if opaqueOnly {
		prior := "queued"
		if claimEarly {
			prior = "running"
		}
		tag, e := tx.Exec(ctx, `UPDATE wm_full_journal_batches SET state='done',raw_output=$2,model='未调用模型',input_tokens=NULL,output_tokens=NULL,error='',updated_at=now() WHERE id=$1 AND state=$3`, id, opaqueNote, prior)
		if e != nil || tag.RowsAffected() != 1 {
			return false, errors.New("coverage record failed")
		}
		if e = tx.Commit(ctx); e != nil {
			return false, e
		}
		return true, nil
	}
	if gap {
		material = append([]byte("注意：此批含图片或音视频，本次仅分析文字，媒体内容尚未分析；原文保留。\n"), material...)
	}
	if !claimEarly {
		tag, e := pool.Exec(ctx, `UPDATE wm_full_journal_batches SET state='running',started_at=now(),updated_at=now() WHERE id=$1 AND state='queued'`, id)
		if e != nil || tag.RowsAffected() != 1 {
			return false, errors.New("batch claim failed")
		}
	}
	modelStarted = true
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	output, modelName, usage, callErr := model.Extract(callCtx, string(material))
	cancel()
	if callErr == nil && !CitationsKnown(output, batch) {
		output = "[来源未核验：模型未给出可识别的来源编号，或引用了批次之外的编号；需人工核对。]\n" + output
	}
	if gap && callErr == nil {
		output = "[覆盖缺口：本批媒体内容未分析，原文及来源位置已保留。]\n" + output
	}
	message := ""
	state := "done"
	if callErr != nil {
		message = "模型调用失败或结果未知，需人工检查"
		state = "failed"
	}
	_, err = tx.Exec(ctx, `INSERT INTO wm_full_journal_attempts(batch_id,model,raw_output,input_tokens,output_tokens,error) VALUES($1,$2,$3,$4,$5,$6)`, id, modelName, output, usage.Input, usage.Output, message)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE wm_full_journal_batches SET state=$2,raw_output=$3,model=$4,input_tokens=$5,output_tokens=$6,error=$7,updated_at=now() WHERE id=$1 AND state='running'`, id, state, output, modelName, usage.Input, usage.Output, message)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	if callErr != nil {
		return true, callErr
	}
	return true, nil
}
