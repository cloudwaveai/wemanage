package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type knowledgeSource struct {
	Ordinal     int       `json:"id"`
	Day         string    `json:"day"`
	EmployeeID  int64     `json:"employee_id"`
	Name        string    `json:"name"`
	Summary     string    `json:"summary"`
	Model       string    `json:"digest_model"`
	GeneratedAt time.Time `json:"generated_at"`
}
type knowledgeItem struct {
	ID              int64           `json:"id"`
	Category        string          `json:"category"`
	Title           string          `json:"title"`
	Content         string          `json:"content"`
	Project         string          `json:"project_hint"`
	Evidence        json.RawMessage `json:"evidence"`
	Verified        bool            `json:"evidence_verified"`
	Status          string          `json:"status"`
	ReviewedTitle   *string         `json:"reviewed_title"`
	ReviewedContent *string         `json:"reviewed_content"`
}
type knowledgeAttempt struct {
	Model  string    `json:"model"`
	Raw    string    `json:"raw_output"`
	Input  *int64    `json:"input_tokens"`
	Output *int64    `json:"output_tokens"`
	Error  string    `json:"error"`
	At     time.Time `json:"created_at"`
}
type knowledgeRun struct {
	Version    int                `json:"version"`
	ID         int64              `json:"id"`
	Kind       string             `json:"kind"`
	From       string             `json:"from"`
	To         string             `json:"to_exclusive"`
	Status     string             `json:"status"`
	Model      string             `json:"model"`
	Warning    string             `json:"warning"`
	Error      string             `json:"error"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at"`
	Sources    []knowledgeSource  `json:"sources"`
	Items      []knowledgeItem    `json:"items"`
	Attempts   []knowledgeAttempt `json:"attempts"`
}

const knowledgeVisible = `NOT EXISTS (SELECT 1 FROM wm_knowledge_sources s JOIN employees subject ON subject.id=s.employee_id WHERE s.run_id=run.id AND s.employee_id<>$1 AND (subject.private_work_since IS NOT NULL OR subject.name='林伊默'))`

func (s *Server) handleKnowledgeRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := currentEmployee(r).ID
	rows, err := s.deps.Store.Pool.Query(ctx, `SELECT run.id,run.version,run.kind,run.period_start::text,run.period_end::text,run.status,run.model,run.warning,run.error,run.started_at,run.finished_at FROM wm_knowledge_runs run WHERE `+knowledgeVisible+` ORDER BY run.period_start DESC,run.kind,run.id DESC`, viewer)
	if err != nil {
		writeErr(w, 500, "读取知识整理失败")
		return
	}
	defer rows.Close()
	out := []knowledgeRun{}
	for rows.Next() {
		var x knowledgeRun
		if rows.Scan(&x.ID, &x.Version, &x.Kind, &x.From, &x.To, &x.Status, &x.Model, &x.Warning, &x.Error, &x.StartedAt, &x.FinishedAt) != nil {
			writeErr(w, 500, "读取知识整理失败")
			return
		}
		x.Sources = []knowledgeSource{}
		x.Items = []knowledgeItem{}
		x.Attempts = []knowledgeAttempt{}
		out = append(out, x)
	}
	if rows.Err() != nil {
		writeErr(w, 500, "读取知识整理失败")
		return
	}
	rows.Close()
	for i := range out {
		sr, err := s.deps.Store.Pool.Query(ctx, `SELECT x.ordinal,x.source_day::text,x.employee_id,x.employee_name,x.summary_md,x.digest_model,x.digest_generated_at FROM wm_knowledge_sources x JOIN employees e ON e.id=x.employee_id WHERE x.run_id=$1 ORDER BY x.ordinal`, out[i].ID)
		if err != nil {
			writeErr(w, 500, "读取知识来源失败")
			return
		}
		for sr.Next() {
			var x knowledgeSource
			if sr.Scan(&x.Ordinal, &x.Day, &x.EmployeeID, &x.Name, &x.Summary, &x.Model, &x.GeneratedAt) != nil {
				sr.Close()
				writeErr(w, 500, "读取知识来源失败")
				return
			}
			out[i].Sources = append(out[i].Sources, x)
		}
		if sr.Err() != nil {
			sr.Close()
			writeErr(w, 500, "读取知识来源失败")
			return
		}
		sr.Close()
		ir, err := s.deps.Store.Pool.Query(ctx, `SELECT i.id,i.category,i.title,i.content,i.project_hint,i.evidence,i.evidence_verified,i.status,i.reviewed_title,i.reviewed_content FROM wm_knowledge_items i LEFT JOIN employees reviewer ON reviewer.id=i.reviewed_by WHERE i.run_id=$2 AND (i.reviewed_by IS NULL OR i.reviewed_by=$1 OR (reviewer.private_work_since IS NULL AND reviewer.name <> '林伊默')) ORDER BY i.id`, viewer, out[i].ID)
		if err != nil {
			writeErr(w, 500, "读取知识候选失败")
			return
		}
		for ir.Next() {
			var x knowledgeItem
			if ir.Scan(&x.ID, &x.Category, &x.Title, &x.Content, &x.Project, &x.Evidence, &x.Verified, &x.Status, &x.ReviewedTitle, &x.ReviewedContent) != nil {
				ir.Close()
				writeErr(w, 500, "读取知识候选失败")
				return
			}
			out[i].Items = append(out[i].Items, x)
		}
		if ir.Err() != nil {
			ir.Close()
			writeErr(w, 500, "读取知识候选失败")
			return
		}
		ir.Close()
		ar, err := s.deps.Store.Pool.Query(ctx, `SELECT model,raw_output,input_tokens,output_tokens,error,created_at FROM wm_knowledge_attempts WHERE run_id=$1 ORDER BY id DESC`, out[i].ID)
		if err != nil {
			writeErr(w, 500, "读取整理记录失败")
			return
		}
		for ar.Next() {
			var x knowledgeAttempt
			if ar.Scan(&x.Model, &x.Raw, &x.Input, &x.Output, &x.Error, &x.At) != nil {
				ar.Close()
				writeErr(w, 500, "读取整理记录失败")
				return
			}
			out[i].Attempts = append(out[i].Attempts, x)
		}
		if ar.Err() != nil {
			ar.Close()
			writeErr(w, 500, "读取整理记录失败")
			return
		}
		ar.Close()
	}
	writeJSON(w, 200, map[string]any{"runs": out})
}
func (s *Server) handleKnowledgeSchedule(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	var activated time.Time
	err := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT enabled,activated_at FROM wm_knowledge_schedule WHERE id=1`).Scan(&enabled, &activated)
	if err != nil {
		writeErr(w, 500, "读取定时状态失败")
		return
	}
	set := s.deps.Settings.Get(r.Context())
	model := set.WorkDigestModel
	if model == "" {
		model = set.DefaultCodexModel
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "activated_at": activated, "model": model, "configured": s.deps.Knowledge != nil && set.WorkDigestAccountID > 0, "cadence": "约每 30 分钟检查：日报完成后整理前一日；每周一整理上一周"})
}
func (s *Server) handleSetKnowledgeSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Enabled == nil {
		writeErr(w, 400, "请选择启用或暂停")
		return
	}
	_, err := s.deps.Store.Pool.Exec(r.Context(), `UPDATE wm_knowledge_schedule SET enabled=$1,updated_at=now() WHERE id=1`, *in.Enabled)
	if err != nil {
		writeErr(w, 500, "保存定时状态失败")
		return
	}
	s.audit(r, "wm_knowledge_schedule", "wm_knowledge", "1", map[string]any{"enabled": *in.Enabled})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) handleKnowledgeRun(w http.ResponseWriter, r *http.Request) {
	if s.deps.Knowledge == nil {
		writeErr(w, 503, "知识整理尚未配置")
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || (in.Kind != "daily" && in.Kind != "weekly") {
		writeErr(w, 400, "请选择日报或周报整理")
		return
	}
	now := time.Now()
	var from, to time.Time
	if in.Kind == "daily" {
		to = s.deps.Knowledge.Day(now)
		from = to.AddDate(0, 0, -1)
	} else {
		to = s.deps.Knowledge.Week(now)
		from = to.AddDate(0, 0, -7)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := s.deps.Knowledge.Run(ctx, in.Kind, from, to); err != nil {
		s.deps.Log.Warn("knowledge manual run", "err", err)
		writeErr(w, 502, "整理尚未完成，请查看任务状态、日报和模型配置")
		return
	}
	s.audit(r, "wm_knowledge_manual", "wm_knowledge", in.Kind, map[string]any{"from": from.Format(time.DateOnly), "to_exclusive": to.Format(time.DateOnly)})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) handleKnowledgeReview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "候选编号无效")
		return
	}
	var in struct {
		Status  string `json:"status"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || (in.Status != "confirmed" && in.Status != "rejected") || (in.Status == "confirmed" && (in.Title == "" || in.Content == "")) {
		writeErr(w, 400, "请填写确认内容")
		return
	}
	viewer := currentEmployee(r).ID
	var found int64
	err = s.deps.Store.Pool.QueryRow(r.Context(), `UPDATE wm_knowledge_items i SET status=$3,reviewed_title=$4,reviewed_content=$5,reviewed_by=$1,reviewed_at=now() FROM wm_knowledge_runs run WHERE i.id=$2 AND i.run_id=run.id AND i.status='draft' AND `+knowledgeVisible+` RETURNING i.id`, viewer, id, in.Status, in.Title, in.Content).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, 404, "候选不存在、已处理或无权访问")
		return
	}
	if err != nil {
		writeErr(w, 500, "保存审核失败")
		return
	}
	s.audit(r, "wm_knowledge_review", "wm_knowledge_item", fmt.Sprint(id), map[string]any{"status": in.Status})
	writeJSON(w, 200, map[string]any{"ok": true})
}
