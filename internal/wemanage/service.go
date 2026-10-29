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

type Service struct{ Store *store.Store }
type identityKey struct{}
type identity struct {
	employee store.Employee
	csrf     string
}

// Install adds separate tables; it never modifies or deletes gateway records.
func Install(ctx context.Context, st *store.Store) error {
	_, err := st.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS wm_cases (
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
 );`)
	return err
}

func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Group(func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/api/session", s.session)
		r.Get("/api/projects", s.projects)
		r.Get("/api/projects/{id}/events", s.events)
		r.Get("/api/projects/{id}/activity", s.activity)
		r.Get("/api/coverage", s.coverage)
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
			fail(w, 401, "请先登录网关")
			return
		}
		sess, err := s.Store.GetWebSession(r.Context(), c.Value)
		if err != nil {
			fail(w, 401, "登录已过期，请重新登录网关")
			return
		}
		emp, err := s.Store.GetEmployee(r.Context(), sess.EmployeeID)
		if err != nil || !emp.IsSuperAdmin || emp.Status != "active" {
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
	reply(w, 200, map[string]any{"name": i.employee.Name, "csrf_token": i.csrf})
}
func (s *Service) projects(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Projects(r.Context())
	if err != nil {
		fail(w, 500, "读取项目失败")
		return
	}
	reply(w, 200, map[string]any{"projects": rows})
}
func idParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "项目编号无效")
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
