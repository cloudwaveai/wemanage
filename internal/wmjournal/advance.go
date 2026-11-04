package wmjournal

import (
	"context"
	"encoding/json"
	"errors"

	"aiseat/internal/store"
	"aiseat/internal/workrecords"
)

// AdvanceReduction performs one non-extraction step under the old scheduler's
// lock. The caller must use ProcessOne for extraction.
func AdvanceReduction(ctx context.Context, st *store.Store, run int64, model workrecords.ModelExtractor, sol string) (bool, error) {
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked, enabled bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil || !locked {
		return false, errors.New("scheduler unavailable")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	if err = conn.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled); err != nil || enabled {
		return false, errors.New("scheduler must remain paused")
	}
	var phase string
	var level, queued, unfinished int
	err = conn.QueryRow(ctx, `SELECT phase,level,count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state IN ('running','failed')) FROM wm_full_journal_batches WHERE run_id=$1 GROUP BY phase,level ORDER BY CASE phase WHEN 'synthesis' THEN 4 WHEN 'daily' THEN 3 WHEN 'reduce' THEN 2 ELSE 1 END DESC,level DESC LIMIT 1`, run).Scan(&phase, &level, &queued, &unfinished)
	if err != nil {
		return false, err
	}
	if unfinished > 0 {
		return false, errors.New("batch failed or outcome unknown")
	}
	if queued > 0 {
		if phase == "extract" {
			return false, errors.New("extraction still pending")
		}
		return (&workrecords.Queue{Store: st}).ProcessReduction(ctx, run, phase, level, model, sol)
	}
	switch phase {
	case "extract", "reduce":
		_, _, err = PrepareReduction(ctx, st, run, phase, level)
		return err == nil, err
	case "daily":
		err = prepareSynthesis(ctx, st, run)
		return err == nil, err
	case "synthesis":
		err = finish(ctx, st, run)
		return false, err
	default:
		return false, errors.New("unknown phase")
	}
}

func prepareSynthesis(ctx context.Context, st *store.Store, run int64) error {
	var report string
	var gaps, unverified bool
	if err := st.Pool.QueryRow(ctx, `SELECT raw_output,EXISTS(SELECT 1 FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract' AND raw_output LIKE '%[覆盖缺口%'),EXISTS(SELECT 1 FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract' AND raw_output LIKE '%[来源未核验%') FROM wm_full_journal_batches WHERE run_id=$1 AND phase='daily' AND level=0 AND ordinal=0 AND state='done'`, run).Scan(&report, &gaps, &unverified); err != nil {
		return err
	}
	if gaps {
		report = "**覆盖说明：部分媒体内容尚未分析，原始记录及位置已保留。以下日报不能代表这些内容。**\n\n" + report
	}
	if unverified {
		report = "**证据说明：部分批次的来源编号未通过自动核验，相关内容需人工核对。**\n\n" + report
	}
	payload, err := json.Marshal(map[string]string{"daily_report": report})
	if err != nil {
		return err
	}
	if len(payload) > 100000 {
		return errors.New("daily report exceeds synthesis capacity")
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET daily_report=$2,updated_at=now() WHERE id=$1`, run, report); err != nil {
		return err
	}
	var same bool
	err = tx.QueryRow(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,level,ordinal,input_manifest,state) VALUES($1,'synthesis',0,0,$2,'queued') ON CONFLICT(run_id,phase,level,ordinal) DO UPDATE SET input_manifest=wm_full_journal_batches.input_manifest RETURNING input_manifest=$2::jsonb`, run, payload).Scan(&same)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("existing synthesis differs")
	}
	return tx.Commit(ctx)
}

func finish(ctx context.Context, st *store.Store, run int64) error {
	tag, err := st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs r SET synthesis=s.raw_output,state=CASE WHEN r.missing_count>0 OR EXISTS (SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.phase='extract' AND (b.raw_output LIKE '%[覆盖缺口%' OR b.raw_output LIKE '%[来源未核验%')) OR EXISTS (SELECT 1 FROM requests req JOIN employees emp ON emp.id=req.employee_id WHERE req.started_at >= (r.day::timestamp AT TIME ZONE 'Asia/Shanghai') AND req.started_at < ((r.day+1)::timestamp AT TIME ZONE 'Asia/Shanghai') AND emp.private_work_since IS NULL AND emp.name <> '林伊默' AND NOT EXISTS (SELECT 1 FROM wm_full_journal_sources src WHERE src.run_id=r.id AND src.request_id=req.id)) THEN 'partial' ELSE 'done' END,updated_at=now() FROM wm_full_journal_batches s WHERE r.id=$1 AND r.daily_report<>'' AND s.run_id=r.id AND s.phase='synthesis' AND s.state='done' AND NOT EXISTS (SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.state<>'done') AND NOT EXISTS (WITH RECURSIVE lineage(id,previous_run_id) AS (SELECT r.id,r.previous_run_id UNION ALL SELECT p.id,p.previous_run_id FROM wm_full_journal_runs p JOIN lineage l ON p.id=l.previous_run_id) SELECT 1 FROM wm_full_journal_sources x JOIN employees e ON e.id=x.employee_id WHERE x.run_id IN (SELECT id FROM lineage) AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("journal incomplete or source privacy changed")
	}
	return nil
}
