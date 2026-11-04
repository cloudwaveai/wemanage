package wemanage

import (
	"encoding/json"
	"net/http"
)

const visibleKnowledge = `NOT EXISTS (WITH RECURSIVE lineage(id,previous_run_id) AS (SELECT r.id,r.previous_run_id UNION ALL SELECT p.id,p.previous_run_id FROM wm_full_journal_runs p JOIN lineage l ON p.id=l.previous_run_id) SELECT 1 FROM wm_full_journal_sources x JOIN employees e ON e.id=x.employee_id WHERE x.run_id IN (SELECT id FROM lineage) AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`

func (s *Service) logKnowledgeRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT k.id,r.day::text,k.state,k.model,k.raw_output,k.input_tokens,k.output_tokens,k.error,COALESCE((SELECT jsonb_agg(jsonb_build_object('model',a.model,'raw_output',a.raw_output,'input_tokens',a.input_tokens,'output_tokens',a.output_tokens,'error',a.error,'created_at',a.created_at) ORDER BY a.id) FROM wm_log_knowledge_attempts a WHERE a.knowledge_run_id=k.id),'[]'::jsonb) FROM wm_log_knowledge_runs k JOIN wm_full_journal_runs r ON r.id=k.journal_run_id WHERE `+visibleKnowledge+` ORDER BY r.day DESC,k.id DESC`)
	if err != nil {
		fail(w, 500, "读取提取记录失败")
		return
	}
	defer rows.Close()
	type entry struct {
		ID       int64           `json:"id"`
		Day      string          `json:"day"`
		State    string          `json:"state"`
		Model    string          `json:"model"`
		Raw      string          `json:"raw_output"`
		Input    *int64          `json:"input_tokens"`
		Output   *int64          `json:"output_tokens"`
		Error    string          `json:"error"`
		Attempts json.RawMessage `json:"attempts"`
	}
	out := []entry{}
	for rows.Next() {
		var v entry
		if rows.Scan(&v.ID, &v.Day, &v.State, &v.Model, &v.Raw, &v.Input, &v.Output, &v.Error, &v.Attempts) != nil {
			fail(w, 500, "读取提取记录失败")
			return
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取提取记录失败")
		return
	}
	reply(w, 200, map[string]any{"runs": out})
}

func (s *Service) retryLogKnowledge(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "任务编号无效")
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
	tag, err := tx.Exec(r.Context(), `UPDATE wm_log_knowledge_runs k SET state='queued',error='' FROM wm_full_journal_runs r WHERE k.id=$1 AND k.journal_run_id=r.id AND (k.state='failed' OR (k.state='running' AND k.started_at < now()-interval '5 minutes')) AND `+visibleKnowledge, id)
	if err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "任务不存在、仍在运行或无权查看")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, 500, "恢复失败")
		return
	}
	reply(w, 200, map[string]any{"status": "queued"})
}

func (s *Service) logKnowledge(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT c.id,c.category,c.title,c.content,c.evidence,c.status,r.day::text,k.model FROM wm_log_knowledge_cards c JOIN wm_log_knowledge_runs k ON k.id=c.knowledge_run_id JOIN wm_full_journal_runs r ON r.id=k.journal_run_id WHERE k.state='done' AND `+visibleKnowledge+` ORDER BY r.day DESC,c.id`)
	if err != nil {
		fail(w, 500, "读取知识候选失败")
		return
	}
	defer rows.Close()
	type card struct {
		ID       int64           `json:"id"`
		Category string          `json:"category"`
		Title    string          `json:"title"`
		Content  string          `json:"content"`
		Evidence json.RawMessage `json:"evidence"`
		Status   string          `json:"status"`
		Day      string          `json:"day"`
		Model    string          `json:"model"`
	}
	result := []card{}
	for rows.Next() {
		var c card
		if rows.Scan(&c.ID, &c.Category, &c.Title, &c.Content, &c.Evidence, &c.Status, &c.Day, &c.Model) != nil {
			fail(w, 500, "读取知识候选失败")
			return
		}
		result = append(result, c)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取知识候选失败")
		return
	}
	reply(w, 200, map[string]any{"cards": result})
}

func (s *Service) reviewLogKnowledge(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "候选编号无效")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || (body.Status != "confirmed" && body.Status != "rejected" && body.Status != "draft") {
		fail(w, 400, "状态无效")
		return
	}
	tag, err := s.Store.Pool.Exec(r.Context(), `UPDATE wm_log_knowledge_cards c SET status=$2,reviewed_by=$3,reviewed_at=now() FROM wm_log_knowledge_runs k JOIN wm_full_journal_runs r ON r.id=k.journal_run_id WHERE c.id=$1 AND c.knowledge_run_id=k.id AND k.state='done' AND `+visibleKnowledge, id, body.Status, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存审核状态失败")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "候选不存在或无权查看")
		return
	}
	reply(w, 200, map[string]any{"status": body.Status})
}
