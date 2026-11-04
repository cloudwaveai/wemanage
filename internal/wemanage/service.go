// Package wemanage provides the independent, session-protected analysis service.
package wemanage

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

//go:embed static/*
var assets embed.FS

type Service struct {
	Store      *store.Store
	Summarizer SummaryModel
}
type identityKey struct{}
type identity struct {
	employee store.Employee
	csrf     string
}

// Install adds separate tables; it never modifies or deletes gateway records.
func Install(ctx context.Context, st *store.Store) error {
	_, err := st.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS wm_custom_summary_settings (employee_id BIGINT PRIMARY KEY REFERENCES employees(id), prompt TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS wm_model_price_catalog (model TEXT PRIMARY KEY, currency TEXT NOT NULL, input_per_million NUMERIC NOT NULL, cached_per_million NUMERIC NOT NULL, output_per_million NUMERIC NOT NULL, source TEXT NOT NULL, checked_on DATE NOT NULL);
 CREATE TABLE IF NOT EXISTS wm_model_price_overrides (id BIGSERIAL PRIMARY KEY, model TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('manual','standard')), currency TEXT NOT NULL, input_per_million NUMERIC NOT NULL, cached_per_million NUMERIC NOT NULL, output_per_million NUMERIC NOT NULL, effective_at TIMESTAMPTZ NOT NULL, created_by BIGINT NOT NULL REFERENCES employees(id), created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE INDEX IF NOT EXISTS wm_price_override_lookup ON wm_model_price_overrides(model,effective_at DESC,id DESC);
 INSERT INTO wm_model_price_catalog VALUES
 ('gpt-6-astra','USD',10,1,50,'https://developers.openai.com/api/docs/models/gpt-6-astra','2026-11-03'),
 ('gpt-6.1-sol','USD',2,0.10,10,'https://developers.openai.com/api/docs/models/gpt-6.1-sol','2026-11-03'),
 ('gpt-6.1-terra','USD',1,0.05,5,'https://developers.openai.com/api/docs/models/gpt-6.1-terra','2026-11-03'),
 ('gpt-6-luna','USD',0.10,0.01,0.50,'https://developers.openai.com/api/docs/models/gpt-6-luna','2026-11-03'),
 ('gpt-6-sol','USD',2,0.20,10,'https://developers.openai.com/api/docs/models/gpt-6-sol','2026-11-03'),
 ('gpt-5.6-sol','USD',4,0.40,20,'https://developers.openai.com/api/docs/models/gpt-5.6-sol','2026-11-03'),
 ('gpt-5.6-terra','USD',2,0.20,12,'https://developers.openai.com/api/docs/models/gpt-5.6-terra','2026-11-03'),
 ('gpt-5.6-luna','USD',0.20,0.02,1.20,'https://developers.openai.com/api/docs/models/gpt-5.6-luna','2026-11-03') ON CONFLICT(model) DO NOTHING;
 CREATE TABLE IF NOT EXISTS wm_custom_summaries (id BIGSERIAL PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES employees(id), start_day DATE NOT NULL, end_day DATE NOT NULL, prompt TEXT NOT NULL, result TEXT NOT NULL, model TEXT NOT NULL, input_tokens BIGINT, output_tokens BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS wm_custom_summary_sources (report_id BIGINT NOT NULL REFERENCES wm_custom_summaries(id), employee_id BIGINT NOT NULL REFERENCES employees(id), source_day DATE NOT NULL, summary TEXT NOT NULL, PRIMARY KEY(report_id, employee_id, source_day));
 ALTER TABLE wm_custom_summaries ADD COLUMN IF NOT EXISTS project_name TEXT NOT NULL DEFAULT '';
 CREATE TABLE IF NOT EXISTS wm_project_report_items (report_id BIGINT NOT NULL REFERENCES wm_custom_summaries(id), item_id BIGINT NOT NULL REFERENCES wm_knowledge_items(id), PRIMARY KEY(report_id,item_id));
 ALTER TABLE wm_custom_summaries ADD COLUMN IF NOT EXISTS subject_id BIGINT REFERENCES employees(id);
 ALTER TABLE wm_custom_summaries ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'done';
 ALTER TABLE wm_custom_summaries ADD COLUMN IF NOT EXISTS error TEXT NOT NULL DEFAULT '';
 CREATE UNIQUE INDEX IF NOT EXISTS wm_custom_summary_one_running ON wm_custom_summaries(owner_id) WHERE state='running';
 CREATE TABLE IF NOT EXISTS wm_project_candidate_aliases (alias TEXT PRIMARY KEY, canonical TEXT NOT NULL, updated_by BIGINT NOT NULL REFERENCES employees(id), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS wm_project_candidate_states (name TEXT PRIMARY KEY, hidden BOOLEAN NOT NULL DEFAULT false, updated_by BIGINT NOT NULL REFERENCES employees(id), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS wm_cases (
  id BIGSERIAL PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id),
  event_id BIGINT NOT NULL REFERENCES project_events(id), created_by BIGINT NOT NULL REFERENCES employees(id),
  title TEXT NOT NULL, situation TEXT NOT NULL, approach TEXT NOT NULL, outcome TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','confirmed')),
  confirmed_by BIGINT REFERENCES employees(id), confirmed_at TIMESTAMPTZ,
  updated_by BIGINT REFERENCES employees(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 ALTER TABLE wm_cases ADD COLUMN IF NOT EXISTS updated_by BIGINT REFERENCES employees(id);
 CREATE INDEX IF NOT EXISTS wm_cases_project_idx ON wm_cases(project_id,created_at DESC);
 CREATE TABLE IF NOT EXISTS wm_case_revisions (
  id BIGSERIAL PRIMARY KEY, case_id BIGINT NOT NULL REFERENCES wm_cases(id),
  changed_by BIGINT NOT NULL REFERENCES employees(id), content_author BIGINT NOT NULL REFERENCES employees(id), title TEXT NOT NULL,
  situation TEXT NOT NULL, approach TEXT NOT NULL, outcome TEXT NOT NULL,
  state TEXT NOT NULL, confirmed_by BIGINT REFERENCES employees(id), confirmed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_feedback (
  id BIGSERIAL PRIMARY KEY, case_id BIGINT NOT NULL REFERENCES wm_cases(id),
  created_by BIGINT NOT NULL REFERENCES employees(id), content TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_hidden_projects (
  project_id BIGINT PRIMARY KEY REFERENCES projects(id),
  reason TEXT NOT NULL,
  hidden_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_log_worker_control (
  id INTEGER PRIMARY KEY CHECK (id=1), enabled BOOLEAN NOT NULL DEFAULT false,
  activated_at TIMESTAMPTZ NOT NULL DEFAULT now(), from_day DATE NOT NULL DEFAULT ((now() AT TIME ZONE 'Asia/Shanghai')::date - 1), last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 INSERT INTO wm_log_worker_control (id) VALUES(1) ON CONFLICT(id) DO NOTHING;
 CREATE TABLE IF NOT EXISTS wm_log_knowledge_runs (
  id BIGSERIAL PRIMARY KEY, journal_run_id BIGINT NOT NULL UNIQUE REFERENCES wm_full_journal_runs(id),
  state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed')), model TEXT NOT NULL DEFAULT '',
  raw_output TEXT NOT NULL DEFAULT '', input_tokens BIGINT, output_tokens BIGINT,
  error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), started_at TIMESTAMPTZ NOT NULL DEFAULT now(), finished_at TIMESTAMPTZ
 );
 CREATE TABLE IF NOT EXISTS wm_log_knowledge_attempts (
  id BIGSERIAL PRIMARY KEY, knowledge_run_id BIGINT NOT NULL REFERENCES wm_log_knowledge_runs(id),
  raw_output TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,
  error TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_log_knowledge_cards (
  id BIGSERIAL PRIMARY KEY, knowledge_run_id BIGINT NOT NULL REFERENCES wm_log_knowledge_runs(id),
  category TEXT NOT NULL CHECK(category IN ('project','problem','practice','skill')),
  title TEXT NOT NULL, content TEXT NOT NULL, evidence JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','confirmed','rejected')),
  reviewed_by BIGINT REFERENCES employees(id), reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_log_period_runs (
  id BIGSERIAL PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('monthly','quarterly')),
  period_start DATE NOT NULL, period_end DATE NOT NULL, coverage_start DATE NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('planning','running','done','partial','failed')), coverage_partial BOOLEAN NOT NULL DEFAULT false,
  report TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(kind,period_start)
 );
 CREATE TABLE IF NOT EXISTS wm_log_period_sources (
  period_run_id BIGINT NOT NULL REFERENCES wm_log_period_runs(id), journal_run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
  PRIMARY KEY(period_run_id,journal_run_id)
 );
 CREATE TABLE IF NOT EXISTS wm_log_period_people (
  period_run_id BIGINT NOT NULL REFERENCES wm_log_period_runs(id), employee_id BIGINT NOT NULL REFERENCES employees(id),
  PRIMARY KEY(period_run_id,employee_id)
 );
 CREATE TABLE IF NOT EXISTS wm_log_period_batches (
  id BIGSERIAL PRIMARY KEY, period_run_id BIGINT NOT NULL REFERENCES wm_log_period_runs(id),
  level INTEGER NOT NULL, ordinal INTEGER NOT NULL, is_final BOOLEAN NOT NULL,
  input_manifest JSONB NOT NULL, state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed')),
  raw_output TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', input_tokens BIGINT, output_tokens BIGINT,
  error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(period_run_id,level,ordinal)
 );
 CREATE TABLE IF NOT EXISTS wm_log_period_attempts (
  id BIGSERIAL PRIMARY KEY, batch_id BIGINT NOT NULL REFERENCES wm_log_period_batches(id),
  raw_output TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,
  error TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_focus_jobs (
  id BIGSERIAL PRIMARY KEY,item_id BIGINT NOT NULL REFERENCES wm_knowledge_items(id),request_id UUID NOT NULL,keyword TEXT NOT NULL,
  created_by BIGINT NOT NULL REFERENCES employees(id),state TEXT NOT NULL DEFAULT 'queued',result TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',input_snapshot TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS wm_skill_drafts (
 id BIGSERIAL PRIMARY KEY,source_item_id BIGINT NOT NULL REFERENCES wm_knowledge_items(id),variant TEXT NOT NULL,
 source_focus_id BIGINT REFERENCES wm_focus_jobs(id),title TEXT NOT NULL,trigger_text TEXT NOT NULL,steps JSONB NOT NULL,
 verification TEXT NOT NULL,limits_text TEXT NOT NULL,skill_md TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','confirmed','rejected')),
 reviewed_by BIGINT REFERENCES employees(id),reviewed_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(source_item_id,variant)
 );
 CREATE TABLE IF NOT EXISTS wm_focus_chunks (
  id BIGSERIAL PRIMARY KEY,job_id BIGINT NOT NULL REFERENCES wm_focus_jobs(id),ordinal INT NOT NULL,phase TEXT NOT NULL,
  state TEXT NOT NULL,input TEXT NOT NULL,output TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(job_id,phase,ordinal)
 );
 CREATE UNIQUE INDEX IF NOT EXISTS wm_focus_active ON wm_focus_jobs(item_id,request_id,keyword) WHERE state IN ('queued','running','done');
 CREATE TABLE IF NOT EXISTS wm_compact_jobs (
  id BIGSERIAL PRIMARY KEY,run_id BIGINT NOT NULL,employee_id BIGINT NOT NULL DEFAULT 0,session_id BIGINT NOT NULL DEFAULT 0,
  phase TEXT NOT NULL,level INT NOT NULL DEFAULT 0,ordinal INT NOT NULL,state TEXT NOT NULL DEFAULT 'queued',input TEXT NOT NULL,
  output TEXT NOT NULL DEFAULT '',model TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(run_id,employee_id,session_id,phase,level,ordinal)
 );
 CREATE TABLE IF NOT EXISTS wm_log_report_previews (
  id BIGSERIAL PRIMARY KEY,journal_run_id BIGINT NOT NULL REFERENCES wm_full_journal_runs(id),
  session_id BIGINT NOT NULL,state TEXT NOT NULL CHECK(state IN ('running','done','failed')),
  source_batch_ids BIGINT[] NOT NULL,input_snapshot JSONB NOT NULL,raw_output TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',input_tokens BIGINT,output_tokens BIGINT,error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),finished_at TIMESTAMPTZ
 );`+assetSchema)
	return err
}

func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Group(func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/api/session", s.session)
		r.Get("/api/shared/assets", s.assetList)
		r.Get("/api/assets", s.assetList)
		r.Post("/api/assets", s.assetSave)
		r.Post("/api/assets/seed", s.assetSeed)
		r.Get("/api/assets/{id}/history", s.assetHistory)
		r.Get("/api/projects", s.projects)
		r.Get("/api/projects/{id}/events", s.events)
		r.Get("/api/projects/{id}/activity", s.activity)
		r.Get("/api/coverage", s.coverage)
		r.Get("/api/reporting/usage", s.usageReport)
		r.Get("/api/model-prices", s.modelPrices)
		r.Post("/api/model-prices", s.saveModelPrice)
		r.Get("/api/workspace", s.workspaceStats)
		r.Get("/api/organization", s.organizationDirectory)
		r.Get("/api/project-candidates", s.projectCandidates)
		r.Get("/api/project-report", s.getCustomSummary)
		r.Post("/api/project-report/generate", s.generateCustomSummary)
		r.Put("/api/project-candidates", s.updateProjectCandidate)
		r.Get("/api/person-profile", s.getCustomSummary)
		r.Post("/api/person-profile/generate", s.generateCustomSummary)
		r.Get("/api/custom-summary", s.getCustomSummary)
		r.Put("/api/custom-summary", s.saveCustomSummary)
		r.Post("/api/custom-summary/generate", s.generateCustomSummary)
		r.Get("/api/log-worker", s.logWorkerStatus)
		r.Put("/api/log-worker", s.setLogWorkerStatus)
		r.Get("/api/log-knowledge", s.logKnowledge)
		r.Get("/api/log-knowledge/runs", s.logKnowledgeRuns)
		r.Post("/api/log-knowledge/runs/{id}/retry", s.retryLogKnowledge)
		r.Put("/api/log-knowledge/{id}", s.reviewLogKnowledge)
		r.Get("/api/log-reviews", s.logReviews)
		r.Get("/api/log-previews", s.logPreviews)
		r.Get("/api/compact-reports", s.compactReports)
		r.Get("/api/skill-drafts", s.listSkillDrafts)
		r.Put("/api/skill-drafts/{id}", s.reviewSkillDraft)
		r.Get("/api/focus", s.listFocus)
		r.Get("/api/focus/{id}/search", s.searchFocus)
		r.Post("/api/focus/{id}", s.createFocus)
		r.Post("/api/log-reviews/{id}/retry", s.retryLogReview)
		r.Get("/api/cases", s.cases)
		r.Post("/api/cases", s.createCase)
		r.Post("/api/cases/{id}/confirm", s.confirmCase)
		r.Post("/api/cases/{id}/edit", s.editCase)
		r.Post("/api/cases/{id}/feedback", s.feedback)
	})
	static, _ := fs.Sub(assets, "static")
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.FileServer(http.FS(static)).ServeHTTP(w, r)
	})
	return r
}
func reply(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func fail(w http.ResponseWriter, code int, message string) {
	reply(w, code, map[string]string{"error": message})
}
func (s *Service) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("aiseat_sess")
		if err != nil || c.Value == "" {
			fail(w, 401, "请先使用飞书登录")
			return
		}
		sess, err := s.Store.GetWebSession(r.Context(), c.Value)
		if err != nil {
			fail(w, 401, "登录已过期，请重新使用飞书登录")
			return
		}
		emp, err := s.Store.GetEmployee(r.Context(), sess.EmployeeID)
		memberRead := r.Method == http.MethodGet && (r.URL.Path == "/api/session" || r.URL.Path == "/api/shared/assets")
		if err != nil || (!emp.IsSuperAdmin && !memberRead) || emp.Status != "active" {
			fail(w, 403, "仅限有效的超级管理员访问")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-CSRF-Token") != sess.CSRFToken {
			fail(w, 403, "请求验证失败，请刷新页面")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{emp, sess.CSRFToken})))
	})
}
func who(r *http.Request) identity { return r.Context().Value(identityKey{}).(identity) }
func (s *Service) session(w http.ResponseWriter, r *http.Request) {
	i := who(r)
	reply(w, 200, map[string]any{"name": i.employee.Name, "csrf_token": i.csrf, "admin": i.employee.IsSuperAdmin})
}
func (s *Service) projects(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Projects(r.Context())
	if err != nil {
		fail(w, 500, "读取项目失败")
		return
	}
	hidden, err := s.Store.Pool.Query(r.Context(), `SELECT project_id FROM wm_hidden_projects`)
	if err != nil {
		fail(w, 500, "读取项目失败")
		return
	}
	defer hidden.Close()
	excluded := map[int64]bool{}
	for hidden.Next() {
		var id int64
		if err := hidden.Scan(&id); err != nil {
			fail(w, 500, "读取项目失败")
			return
		}
		excluded[id] = true
	}
	if hidden.Err() != nil {
		fail(w, 500, "读取项目失败")
		return
	}
	visible := rows[:0]
	for _, project := range rows {
		if !excluded[project.ID] {
			visible = append(visible, project)
		}
	}
	reply(w, 200, map[string]any{"projects": visible})
}
func idParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
func (s *Service) visibleProject(w http.ResponseWriter, r *http.Request, id int64) bool {
	var hidden bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM wm_hidden_projects WHERE project_id=$1)`, id).Scan(&hidden); err != nil {
		fail(w, 500, "读取项目失败")
		return false
	}
	if hidden {
		fail(w, 404, "项目不存在")
		return false
	}
	return true
}
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "项目编号无效")
		return
	}
	if !s.visibleProject(w, r, id) {
		return
	}
	rows, err := s.Store.ProjectEvents(r.Context(), id, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "读取项目记录失败")
		return
	}
	reply(w, 200, map[string]any{"events": rows})
}

// activity reports only recorded, explicitly project-attributed gateway calls.
func (s *Service) activity(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "项目编号无效")
		return
	}
	if !s.visibleProject(w, r, id) {
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT (r.started_at AT TIME ZONE 'Asia/Shanghai')::date::text,COALESCE(NULLIF(r.model_requested,''),'未记录'),count(*),COALESCE(sum(r.tokens_input),0),COALESCE(sum(r.tokens_output),0),count(*) FILTER (WHERE r.status_code >= 400) FROM requests r JOIN employees e ON e.id=r.employee_id LEFT JOIN api_keys k ON k.id=r.api_key_id LEFT JOIN request_projects rp ON rp.request_id=r.id WHERE r.started_at >= now()-interval '30 days' AND COALESCE(rp.project_id,k.project_id)=$2 AND (r.employee_id=$1 OR (e.private_work_since IS NULL AND e.name <> '林伊默')) GROUP BY 1,2 ORDER BY 1 DESC,2`, who(r).employee.ID, id)
	if err != nil {
		fail(w, 500, "读取项目调用分析失败")
		return
	}
	defer rows.Close()
	type item struct {
		Day      string `json:"day"`
		Model    string `json:"model"`
		Requests int64  `json:"requests"`
		Input    int64  `json:"tokens_input"`
		Output   int64  `json:"tokens_output"`
		Errors   int64  `json:"errors"`
	}
	out := []item{}
	for rows.Next() {
		var x item
		if rows.Scan(&x.Day, &x.Model, &x.Requests, &x.Input, &x.Output, &x.Errors) != nil {
			fail(w, 500, "读取项目调用分析失败")
			return
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取项目调用分析失败")
		return
	}
	reply(w, 200, map[string]any{"rows": out, "days": 30, "timezone": "Asia/Shanghai"})
}

func (s *Service) coverage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	rows, err := s.Store.CaptureCoverage(r.Context(), now.AddDate(0, 0, -30), now)
	if err != nil {
		fail(w, 500, "读取覆盖情况失败")
		return
	}
	reply(w, 200, map[string]any{"rows": rows, "days": 30})
}

type Case struct {
	ID           int64      `json:"id"`
	ProjectID    int64      `json:"project_id"`
	EventID      int64      `json:"event_id"`
	Title        string     `json:"title"`
	Situation    string     `json:"situation"`
	Approach     string     `json:"approach"`
	Outcome      string     `json:"outcome"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"created_at"`
	ConfirmedAt  *time.Time `json:"confirmed_at"`
	EventTime    time.Time  `json:"event_time"`
	EventContent string     `json:"event_content"`
	Evidence     string     `json:"evidence"`
	EmployeeName string     `json:"employee_name"`
	Feedback     []Feedback `json:"feedback"`
	Revisions    []Revision `json:"revisions"`
}
type Revision struct {
	Title     string    `json:"title"`
	Situation string    `json:"situation"`
	Approach  string    `json:"approach"`
	Outcome   string    `json:"outcome"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}
type Feedback struct {
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	Author    string    `json:"author"`
}

const visibility = `(pe.employee_id=$1 OR (e.private_work_since IS NULL AND e.name <> '林伊默'))`
const caseVisibility = visibility + ` AND (c.created_by=$1 OR (author.private_work_since IS NULL AND author.name <> '林伊默')) AND (COALESCE(c.updated_by,c.created_by)=$1 OR (editor.private_work_since IS NULL AND editor.name <> '林伊默'))`

func (s *Service) cases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := who(r).employee.ID
	rows, err := s.Store.Pool.Query(ctx, `SELECT c.id,c.project_id,c.event_id,c.title,c.situation,c.approach,c.outcome,c.state,c.created_at,c.confirmed_at,pe.occurred_at,pe.content,pe.evidence,e.name FROM wm_cases c JOIN project_events pe ON pe.id=c.event_id JOIN employees e ON e.id=pe.employee_id JOIN employees author ON author.id=c.created_by JOIN employees editor ON editor.id=COALESCE(c.updated_by,c.created_by) WHERE `+caseVisibility+` ORDER BY c.created_at DESC,c.id DESC`, viewer)
	if err != nil {
		fail(w, 500, "读取案例失败")
		return
	}
	defer rows.Close()
	out := []Case{}
	for rows.Next() {
		var c Case
		if rows.Scan(&c.ID, &c.ProjectID, &c.EventID, &c.Title, &c.Situation, &c.Approach, &c.Outcome, &c.State, &c.CreatedAt, &c.ConfirmedAt, &c.EventTime, &c.EventContent, &c.Evidence, &c.EmployeeName) != nil {
			fail(w, 500, "读取案例失败")
			return
		}
		c.Feedback = []Feedback{}
		c.Revisions = []Revision{}
		out = append(out, c)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取案例失败")
		return
	}
	rows.Close()
	// Feedback is checked against current visibility each time, including after privacy changes.
	for i := range out {
		fr, err := s.Store.Pool.Query(ctx, `SELECT f.content,f.created_at,e.name FROM wm_feedback f JOIN employees e ON e.id=f.created_by WHERE f.case_id=$1 AND (f.created_by=$2 OR (e.private_work_since IS NULL AND e.name <> '林伊默')) ORDER BY f.created_at DESC`, out[i].ID, viewer)
		if err != nil {
			fail(w, 500, "读取反馈失败")
			return
		}
		for fr.Next() {
			var f Feedback
			if fr.Scan(&f.Content, &f.CreatedAt, &f.Author) != nil {
				fr.Close()
				fail(w, 500, "读取反馈失败")
				return
			}
			out[i].Feedback = append(out[i].Feedback, f)
		}
		if fr.Err() != nil {
			fr.Close()
			fail(w, 500, "读取反馈失败")
			return
		}
		fr.Close()
		rr, err := s.Store.Pool.Query(ctx, `SELECT v.title,v.situation,v.approach,v.outcome,v.state,v.created_at FROM wm_case_revisions v JOIN employees e ON e.id=v.content_author WHERE v.case_id=$1 AND (v.content_author=$2 OR (e.private_work_since IS NULL AND e.name <> '林伊默')) ORDER BY v.created_at DESC,v.id DESC`, out[i].ID, viewer)
		if err != nil {
			fail(w, 500, "读取历史版本失败")
			return
		}
		for rr.Next() {
			var revision Revision
			if rr.Scan(&revision.Title, &revision.Situation, &revision.Approach, &revision.Outcome, &revision.State, &revision.CreatedAt) != nil {
				rr.Close()
				fail(w, 500, "读取历史版本失败")
				return
			}
			out[i].Revisions = append(out[i].Revisions, revision)
		}
		if rr.Err() != nil {
			rr.Close()
			fail(w, 500, "读取历史版本失败")
			return
		}
		rr.Close()
	}
	reply(w, 200, map[string]any{"cases": out})
}
func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		fail(w, 400, "内容格式无效或超过 1 MiB")
		return false
	}
	return true
}
func valid(s string) bool { return strings.TrimSpace(s) != "" && len([]rune(s)) <= 10000 }
func (s *Service) createCase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID int64  `json:"project_id"`
		EventID   int64  `json:"event_id"`
		Title     string `json:"title"`
		Situation string `json:"situation"`
		Approach  string `json:"approach"`
		Outcome   string `json:"outcome"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if in.ProjectID <= 0 || in.EventID <= 0 || !valid(in.Title) || len([]rune(in.Title)) > 150 || !valid(in.Situation) || !valid(in.Approach) || !valid(in.Outcome) {
		fail(w, 400, "请选择项目记录，并填写标题、背景、方法和结果")
		return
	}
	var id int64
	viewer := who(r).employee.ID
	err := s.Store.Pool.QueryRow(r.Context(), `INSERT INTO wm_cases(project_id,event_id,created_by,title,situation,approach,outcome) SELECT pe.project_id,pe.id,$1,$4,$5,$6,$7 FROM project_events pe JOIN employees e ON e.id=pe.employee_id WHERE pe.id=$2 AND pe.project_id=$3 AND `+visibility+` RETURNING id`, viewer, in.EventID, in.ProjectID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Situation), strings.TrimSpace(in.Approach), strings.TrimSpace(in.Outcome)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "项目记录不存在或无权访问")
		return
	}
	if err != nil {
		fail(w, 500, "保存案例失败")
		return
	}
	s.audit(r, "wm_case_create", id)
	reply(w, 200, map[string]any{"id": id, "state": "draft"})
}

// editCase preserves the previous full version and returns confirmed cases to draft.
func (s *Service) editCase(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "案例编号无效")
		return
	}
	var in struct {
		Title     string `json:"title"`
		Situation string `json:"situation"`
		Approach  string `json:"approach"`
		Outcome   string `json:"outcome"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if !valid(in.Title) || len([]rune(in.Title)) > 150 || !valid(in.Situation) || !valid(in.Approach) || !valid(in.Outcome) {
		fail(w, 400, "请填写标题、背景、方法和结果")
		return
	}
	ctx := r.Context()
	viewer := who(r).employee.ID
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		fail(w, 500, "保存修改失败")
		return
	}
	defer tx.Rollback(ctx)
	var found int64
	err = tx.QueryRow(ctx, `SELECT c.id FROM wm_cases c JOIN project_events pe ON pe.id=c.event_id JOIN employees e ON e.id=pe.employee_id JOIN employees author ON author.id=c.created_by JOIN employees editor ON editor.id=COALESCE(c.updated_by,c.created_by) WHERE c.id=$2 AND `+caseVisibility+` FOR UPDATE OF c`, viewer, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "案例不存在或无权访问")
		return
	}
	if err != nil {
		fail(w, 500, "保存修改失败")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO wm_case_revisions(case_id,changed_by,content_author,title,situation,approach,outcome,state,confirmed_by,confirmed_at) SELECT id,$2,COALESCE(updated_by,created_by),title,situation,approach,outcome,state,confirmed_by,confirmed_at FROM wm_cases WHERE id=$1`, id, viewer)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE wm_cases SET title=$2,situation=$3,approach=$4,outcome=$5,state='draft',confirmed_by=NULL,confirmed_at=NULL,updated_by=$6 WHERE id=$1`, id, strings.TrimSpace(in.Title), strings.TrimSpace(in.Situation), strings.TrimSpace(in.Approach), strings.TrimSpace(in.Outcome), viewer)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		fail(w, 500, "保存修改失败")
		return
	}
	s.audit(r, "wm_case_edit", id)
	reply(w, 200, map[string]any{"ok": true, "state": "draft"})
}

func (s *Service) confirmCase(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "案例编号无效")
		return
	}
	viewer := who(r).employee.ID
	tag, err := s.Store.Pool.Exec(r.Context(), `UPDATE wm_cases c SET state='confirmed',confirmed_by=$1,confirmed_at=now() FROM project_events pe JOIN employees e ON e.id=pe.employee_id, employees author, employees editor WHERE c.id=$2 AND c.event_id=pe.id AND author.id=c.created_by AND editor.id=COALESCE(c.updated_by,c.created_by) AND c.state='draft' AND `+caseVisibility, viewer, id)
	if err != nil {
		fail(w, 500, "确认失败")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "案例不存在、已确认或无权访问")
		return
	}
	s.audit(r, "wm_case_confirm", id)
	reply(w, 200, map[string]any{"ok": true})
}
func (s *Service) feedback(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "案例编号无效")
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if !valid(in.Content) {
		fail(w, 400, "请填写复用反馈，最多 10000 字")
		return
	}
	viewer := who(r).employee.ID
	tag, err := s.Store.Pool.Exec(r.Context(), `INSERT INTO wm_feedback(case_id,created_by,content) SELECT c.id,$1,$3 FROM wm_cases c JOIN project_events pe ON pe.id=c.event_id JOIN employees e ON e.id=pe.employee_id JOIN employees author ON author.id=c.created_by JOIN employees editor ON editor.id=COALESCE(c.updated_by,c.created_by) WHERE c.id=$2 AND c.state='confirmed' AND `+caseVisibility, viewer, id, strings.TrimSpace(in.Content))
	if err != nil {
		fail(w, 500, "保存反馈失败")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "已确认案例不存在或无权访问")
		return
	}
	s.audit(r, "wm_case_feedback", id)
	reply(w, 200, map[string]any{"ok": true})
}
func (s *Service) audit(r *http.Request, action string, id int64) {
	viewer := who(r).employee.ID
	_ = s.Store.Audit(context.WithoutCancel(r.Context()), store.AuditEntry{ActorID: &viewer, Action: action, TargetType: "wm_case", TargetID: fmt.Sprint(id), Detail: map[string]any{}})
}
