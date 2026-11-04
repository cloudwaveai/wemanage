package wmjournal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"aiseat/internal/store"
	"aiseat/internal/workrecords"
)

// PrepareReduction builds a bounded next layer from all completed prior
// batches. Its inputs are log-derived results only, not other daily reports.
func PrepareReduction(ctx context.Context, st *store.Store, run int64, phase string, level int) (string, int, error) {
	if phase != "extract" && phase != "reduce" {
		return "", 0, errors.New("invalid reduction phase")
	}
	var total, done int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state='done') FROM wm_full_journal_batches WHERE run_id=$1 AND phase=$2 AND level=$3`, run, phase, level).Scan(&total, &done); err != nil {
		return "", 0, err
	}
	if total == 0 || total != done {
		return "", 0, errors.New("previous layer incomplete")
	}
	rows, err := st.Pool.Query(ctx, `SELECT id,raw_output FROM wm_full_journal_batches WHERE run_id=$1 AND phase=$2 AND level=$3 ORDER BY ordinal`, run, phase, level)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	var batches []workrecords.PlannedBatch
	planner, err := workrecords.NewPlanner(100000, func(b workrecords.PlannedBatch) error { batches = append(batches, b); return nil })
	if err != nil {
		return "", 0, err
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs r SET previous_run_id=(SELECT p.id FROM wm_full_journal_runs p WHERE p.day=r.day-1 AND p.state IN ('done','partial') AND NOT p.superseded ORDER BY p.version DESC LIMIT 1) WHERE r.id=$1 AND r.previous_run_id IS NULL`, run); err != nil {
		return "", 0, err
	}
	var previousID *int64
	var previousReport *string
	if err = st.Pool.QueryRow(ctx, `SELECT p.id,p.daily_report FROM wm_full_journal_runs r LEFT JOIN wm_full_journal_runs p ON p.id=r.previous_run_id WHERE r.id=$1`, run).Scan(&previousID, &previousReport); err != nil {
		return "", 0, err
	}
	if previousID != nil && previousReport != nil && *previousReport != "" {
		data := []byte(*previousReport)
		seg := workrecords.Segment{Hash: sha256.Sum256(data), Kind: "previous_daily", Data: data}
		if err = planner.Add(workrecords.SourceRecord{SourceID: *previousID, Record: workrecords.Record{Request: []workrecords.Segment{seg}}}); err != nil {
			return "", 0, err
		}
	}
	for rows.Next() {
		var id int64
		var output string
		if err = rows.Scan(&id, &output); err != nil {
			return "", 0, err
		}
		data := []byte(output)
		seg := workrecords.Segment{Hash: sha256.Sum256(data), Kind: "summary", Data: data}
		if err = planner.Add(workrecords.SourceRecord{SourceID: id, Record: workrecords.Record{Request: []workrecords.Segment{seg}}}); err != nil {
			return "", 0, err
		}
	}
	if rows.Err() != nil {
		return "", 0, rows.Err()
	}
	rows.Close()
	if err = planner.Finish(); err != nil {
		return "", 0, err
	}
	if len(batches) == 0 {
		return "", 0, errors.New("no completed results")
	}
	next, nextLevel := "reduce", level+1
	if len(batches) == 1 {
		next, nextLevel = "daily", 0
	}
	if nextLevel > 8 {
		return "", 0, errors.New("reduction depth exceeded")
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(context.Background())
	for i, b := range batches {
		raw, e := json.Marshal(b)
		if e != nil {
			return "", 0, e
		}
		var same bool
		e = tx.QueryRow(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,level,ordinal,input_manifest,state) VALUES($1,$2,$3,$4,$5,'queued') ON CONFLICT(run_id,phase,level,ordinal) DO UPDATE SET input_manifest=wm_full_journal_batches.input_manifest RETURNING input_manifest=$5::jsonb`, run, next, nextLevel, i, raw).Scan(&same)
		if e != nil {
			return "", 0, e
		}
		if !same {
			return "", 0, errors.New("existing reduction differs")
		}
	}
	var existing int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1 AND phase=$2 AND level=$3`, run, next, nextLevel).Scan(&existing); err != nil || existing != len(batches) {
		return "", 0, errors.New("reduction count mismatch")
	}
	if err = tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return next, nextLevel, nil
}
