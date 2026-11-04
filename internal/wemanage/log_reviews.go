package wemanage

import (
	"encoding/json"
	"net/http"
)

func (s *Service) logReviews(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT v.id,v.kind,v.period_start::text,v.period_end::text,v.coverage_start::text,v.state,v.report,v.error,(SELECT count(*) FROM wm_log_period_sources x WHERE x.period_run_id=v.id),(SELECT sum(b.input_tokens) FROM wm_log_period_batches b WHERE b.period_run_id=v.id),(SELECT sum(b.output_tokens) FROM wm_log_period_batches b WHERE b.period_run_id=v.id),COALESCE((SELECT jsonb_agg(jsonb_build_object('level',b.level,'ordinal',b.ordinal,'state',b.state,'model',b.model,'raw_output',b.raw_output,'error',b.error,'attempts',COALESCE((SELECT jsonb_agg(jsonb_build_object('model',a.model,'raw_output',a.raw_output,'input_tokens',a.input_tokens,'output_tokens',a.output_tokens,'error',a.error,'created_at',a.created_at) ORDER BY a.id) FROM wm_log_period_attempts a WHERE a.batch_id=b.id),'[]'::jsonb)) ORDER BY b.level,b.ordinal) FROM wm_log_period_batches b WHERE b.period_run_id=v.id),'[]'::jsonb) FROM wm_log_period_runs v WHERE NOT EXISTS(WITH RECURSIVE lineage(id,previous_run_id) AS (SELECT r.id,r.previous_run_id FROM wm_full_journal_runs r JOIN wm_log_period_sources x ON x.journal_run_id=r.id WHERE x.period_run_id=v.id UNION SELECT p.id,p.previous_run_id FROM wm_full_journal_runs p JOIN lineage l ON p.id=l.previous_run_id) SELECT 1 FROM wm_full_journal_sources src JOIN employees e ON e.id=src.employee_id WHERE src.run_id IN (SELECT id FROM lineage) AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) AND NOT EXISTS(SELECT 1 FROM wm_log_period_people x JOIN employees e ON e.id=x.employee_id WHERE x.period_run_id=v.id AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) ORDER BY v.period_end DESC,v.kind`)
	if err != nil {
		fail(w, 500, "读取周期复盘失败")
		return
	}
	defer rows.Close()
	type item struct {
		ID            int64           `json:"id"`
		Kind          string          `json:"kind"`
		Start         string          `json:"start"`
		End           string          `json:"end_exclusive"`
		CoverageStart string          `json:"coverage_start"`
		State         string          `json:"state"`
		Report        string          `json:"report"`
		Error         string          `json:"error"`
		Sources       int             `json:"sources"`
		Input         *int64          `json:"input_tokens"`
		Output        *int64          `json:"output_tokens"`
		Batches       json.RawMessage `json:"batches"`
	}
	out := []item{}
	for rows.Next() {
		var v item
		if rows.Scan(&v.ID, &v.Kind, &v.Start, &v.End, &v.CoverageStart, &v.State, &v.Report, &v.Error, &v.Sources, &v.Input, &v.Output, &v.Batches) != nil {
			fail(w, 500, "读取周期复盘失败")
			return
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取周期复盘失败")
		return
	}
	reply(w, 200, map[string]any{"reviews": out})
}

func (s *Service) retryLogReview(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "复盘编号无效")
		return
	}
	var body struct {
		AcceptUnknown bool `json:"accept_unknown"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || !body.AcceptUnknown {
		fail(w, 400, "需要确认中断调用可能已计费")
		return
	}
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	defer tx.Rollback(r.Context())
	var paused bool
	if err = tx.QueryRow(r.Context(), `SELECT NOT enabled FROM wm_log_worker_control WHERE id=1 FOR UPDATE`).Scan(&paused); err != nil || !paused {
		fail(w, 409, "请先暂停整理")
		return
	}
	var allowed bool
	err = tx.QueryRow(r.Context(), `SELECT NOT EXISTS(SELECT 1 FROM wm_log_period_people p JOIN employees e ON e.id=p.employee_id WHERE p.period_run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) AND NOT EXISTS(WITH RECURSIVE lineage(id,previous_run_id) AS (SELECT r.id,r.previous_run_id FROM wm_full_journal_runs r JOIN wm_log_period_sources x ON x.journal_run_id=r.id WHERE x.period_run_id=$1 UNION SELECT p.id,p.previous_run_id FROM wm_full_journal_runs p JOIN lineage l ON p.id=l.previous_run_id) SELECT 1 FROM wm_full_journal_sources src JOIN employees e ON e.id=src.employee_id WHERE src.run_id IN (SELECT id FROM lineage) AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) FROM wm_log_period_runs WHERE id=$1 FOR UPDATE`, id).Scan(&allowed)
	if err != nil || !allowed {
		fail(w, 404, "复盘不存在或无权查看")
		return
	}
	var recent bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM wm_log_period_batches WHERE period_run_id=$1 AND state='running' AND updated_at >= now()-interval '5 minutes')`, id).Scan(&recent); err != nil || recent {
		fail(w, 409, "仍有批次正在运行，请稍后检查")
		return
	}
	tag, err := tx.Exec(r.Context(), `UPDATE wm_log_period_batches SET state='queued',error='',updated_at=now() WHERE period_run_id=$1 AND (state='failed' OR (state='running' AND updated_at < now()-interval '5 minutes'))`, id)
	if err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "没有可恢复的批次")
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE wm_log_period_runs SET state='running',error='',updated_at=now() WHERE id=$1`, id); err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	reply(w, 200, map[string]any{"status": "queued"})
}
