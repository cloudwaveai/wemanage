package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// A derivative is visible only while every contributing person's current
// privacy policy permits access. This also covers model outputs and history.
const fullJournalVisible = `NOT EXISTS(WITH RECURSIVE lineage(id,previous_run_id) AS (SELECT r.id,r.previous_run_id UNION ALL SELECT p.id,p.previous_run_id FROM wm_full_journal_runs p JOIN lineage l ON p.id=l.previous_run_id) SELECT 1 FROM wm_full_journal_sources src JOIN employees e ON e.id=src.employee_id WHERE src.run_id IN (SELECT id FROM lineage) AND src.employee_id<>$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`

func (s *Server) handleFullJournal(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	var model string
	if err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT enabled,sol_model FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled, &model); err != nil {
		writeErr(w, 500, "读取设置失败")
		return
	}
	type item struct {
		ID         int64  `json:"id"`
		Day        string `json:"day"`
		Version    int    `json:"version"`
		State      string `json:"state"`
		Sources    int    `json:"sources"`
		Missing    int    `json:"missing"`
		Ready      int    `json:"ready"`
		Planned    int    `json:"planned"`
		Batches    int    `json:"batches"`
		Completed  int    `json:"completed"`
		Errors     int    `json:"errors"`
		Input      *int64 `json:"input_tokens"`
		Output     *int64 `json:"output_tokens"`
		Unreported int    `json:"usage_unreported"`
		Daily      string `json:"daily_report"`
		Synthesis  string `json:"synthesis"`
		Error      string `json:"error"`
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT r.id,r.day::text,r.version,r.state,r.source_count,r.missing_count,(SELECT count(*) FROM wm_full_journal_sources x WHERE x.run_id=r.id AND x.status='ready'),(SELECT count(*) FROM wm_full_journal_sources x WHERE x.run_id=r.id AND x.planned),(SELECT count(*) FROM wm_full_journal_batches b WHERE b.run_id=r.id),(SELECT count(*) FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.state='done'),(SELECT count(*) FROM wm_full_journal_batches b WHERE b.run_id=r.id AND (b.state='failed' OR (b.state='running' AND b.started_at < now()-interval '5 minutes'))),(SELECT sum(a.input_tokens) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=r.id),(SELECT sum(a.output_tokens) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=r.id),(SELECT count(*) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=r.id AND (a.input_tokens IS NULL OR a.output_tokens IS NULL)),r.daily_report,r.synthesis,r.error FROM wm_full_journal_runs r WHERE `+fullJournalVisible+` ORDER BY r.day DESC,r.version DESC`, currentEmployee(r).ID)
	if err != nil {
		s.deps.Log.Error("full journal query", "err", err)
		writeErr(w, 500, "读取全量日报失败")
		return
	}
	defer rows.Close()
	out := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.ID, &x.Day, &x.Version, &x.State, &x.Sources, &x.Missing, &x.Ready, &x.Planned, &x.Batches, &x.Completed, &x.Errors, &x.Input, &x.Output, &x.Unreported, &x.Daily, &x.Synthesis, &x.Error); err != nil {
			s.deps.Log.Error("full journal scan", "err", err)
			writeErr(w, 500, "读取全量日报失败")
			return
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		s.deps.Log.Error("full journal rows", "err", rows.Err())
		writeErr(w, 500, "读取全量日报失败")
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "sol_model": model, "runs": out})
}
func (s *Server) handleFullJournalSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool  `json:"enabled"`
		Model   string `json:"sol_model"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.Enabled == nil {
		writeErr(w, 400, "请选择启用状态")
		return
	}
	model := strings.TrimSpace(in.Model)
	if model == "" || len(model) > 100 || strings.ContainsAny(model, " /\"\n\r\t") {
		writeErr(w, 400, "模型名称无效")
		return
	}
	_, err := s.deps.Store.Pool.Exec(r.Context(), `UPDATE wm_full_journal_settings SET enabled=$1,sol_model=$2,updated_at=now() WHERE id=1`, *in.Enabled, model)
	if err != nil {
		writeErr(w, 500, "保存设置失败")
		return
	}
	s.audit(r, "wm_full_journal_settings", "wm_full_journal", "1", map[string]any{"enabled": *in.Enabled, "model": model})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) handleFullJournalSource(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "来源编号无效")
		return
	}
	var req, day, name, status string
	err = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT src.request_id::text,src.started_at::text,src.employee_name,src.status FROM wm_full_journal_sources src JOIN wm_full_journal_runs r ON r.id=src.run_id WHERE src.id=$2 AND `+fullJournalVisible, currentEmployee(r).ID, id).Scan(&req, &day, &name, &status)
	if err != nil {
		writeErr(w, 404, "来源不存在或无权访问")
		return
	}
	writeJSON(w, 200, map[string]any{"request_id": req, "time": day, "employee_name": name, "status": status})
}

// Retry is an explicit admin action: an interrupted upstream request might
// have been charged even though its response was not recorded.
func (s *Server) handleFullJournalRetry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "任务编号无效")
		return
	}
	var in struct {
		AcceptUnknown bool `json:"accept_unknown"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || !in.AcceptUnknown {
		writeErr(w, 400, "请先确认未知调用可能已经计费")
		return
	}
	tx, err := s.deps.Store.Pool.Begin(r.Context())
	if err != nil {
		writeErr(w, 500, "无法恢复任务")
		return
	}
	defer tx.Rollback(r.Context())
	var tick, worker bool
	if err = tx.QueryRow(r.Context(), `SELECT pg_try_advisory_xact_lock(7419752105),pg_try_advisory_xact_lock(7419752103)`).Scan(&tick, &worker); err != nil || !tick || !worker {
		writeErr(w, 409, "任务正在处理，请稍后重试")
		return
	}
	var state string
	err = tx.QueryRow(r.Context(), `SELECT r.state FROM wm_full_journal_runs r WHERE r.id=$2 AND `+fullJournalVisible+` FOR UPDATE`, currentEmployee(r).ID, id).Scan(&state)
	if err != nil {
		writeErr(w, 404, "任务不存在或无权访问")
		return
	}
	if state != "failed" && state != "running" {
		writeErr(w, 409, "任务不需要恢复")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE wm_full_journal_batches SET state='queued',error='',started_at=NULL,updated_at=now() WHERE run_id=$1 AND (state='failed' OR (state='running' AND started_at < now()-interval '5 minutes'))`, id)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE wm_full_journal_runs r SET state=CASE WHEN NOT r.planning_complete THEN 'preparing' ELSE 'running' END,error='',updated_at=now() WHERE id=$1`, id)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeErr(w, 500, "恢复失败")
		return
	}
	s.audit(r, "wm_full_journal_retry", "wm_full_journal", strconv.FormatInt(id, 10), nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}
