package web

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/store"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Store.Projects(r.Context())
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"projects": list})
}
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "bad body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 || (in.Category != "client" && in.Category != "infrastructure") {
		writeErr(w, 400, "请填写项目名称和类型")
		return
	}
	p, err := s.deps.Store.CreateProject(r.Context(), in.Name, in.Category)
	if err != nil {
		writeErr(w, 500, "cannot create")
		return
	}
	s.audit(r, "project_create", "project", strconv.FormatInt(p.ID, 10), map[string]any{"name": p.Name, "category": p.Category})
	writeJSON(w, 200, p)
}
func (s *Server) handleAssignRequestProject(w http.ResponseWriter, r *http.Request) {
	idb, err := hex.DecodeString(chi.URLParam(r, "id"))
	if err != nil || len(idb) != 16 {
		writeErr(w, 400, "bad id")
		return
	}
	var id [16]byte
	copy(id[:], idb)
	row, err := s.deps.Store.GetRequestFull(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	private, err := s.deps.Store.PrivateWork(r.Context(), row.EmployeeID)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	if private && row.EmployeeID != currentEmployee(r).ID {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		ProjectID int64 `json:"project_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ProjectID <= 0 {
		writeErr(w, 400, "请选择项目")
		return
	}
	ok, err := s.deps.Store.AssignRequestProject(r.Context(), id, in.ProjectID, currentEmployee(r).ID)
	if err != nil {
		writeErr(w, 400, "无法关联项目")
		return
	}
	if !ok {
		writeErr(w, 409, "该记录已属于所选项目")
		return
	}
	s.audit(r, "request_project_assign", "request", hex.EncodeToString(id[:]), map[string]any{"project_id": in.ProjectID})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) handleProjectUsage(w http.ResponseWriter, r *http.Request) {
	loc := s.loc()
	from, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("from"), loc)
	if err != nil {
		writeErr(w, 400, "请选择开始日期")
		return
	}
	last, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("to"), loc)
	if err != nil || last.Before(from) {
		writeErr(w, 400, "请选择有效的结束日期")
		return
	}
	rows, err := s.deps.Store.ProjectUsage(r.Context(), from, last.AddDate(0, 0, 1))
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "from": from.Format(time.DateOnly), "to": last.Format(time.DateOnly)})
}
func (s *Server) handleChannelRates(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	rows, err := s.deps.Store.ChannelRates(r.Context(), id)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"rates": rows})
}
func (s *Server) handleAddChannelRate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var payload struct {
		Model       string    `json:"model"`
		Currency    string    `json:"currency"`
		Input       *float64  `json:"input_per_million"`
		Cached      *float64  `json:"cached_per_million"`
		Output      *float64  `json:"output_per_million"`
		EffectiveAt time.Time `json:"effective_at"`
	}
	if json.NewDecoder(r.Body).Decode(&payload) != nil {
		writeErr(w, 400, "bad body")
		return
	}
	valid := func(v *float64) bool {
		return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0 && *v <= 1000000
	}
	payload.Model = strings.TrimSpace(payload.Model)
	if payload.Model == "" || len(payload.Model) > 200 || (payload.Currency != "CNY" && payload.Currency != "USD") || payload.EffectiveAt.IsZero() || !valid(payload.Input) || !valid(payload.Cached) || !valid(payload.Output) {
		writeErr(w, 400, "请填写有效模型、币种、三个单价和生效时间")
		return
	}
	in := store.ChannelRate{ChannelID: id, Model: payload.Model, Currency: payload.Currency, Input: *payload.Input, Cached: *payload.Cached, Output: *payload.Output, EffectiveAt: payload.EffectiveAt}
	if err := s.deps.Store.AddChannelRate(r.Context(), in); err != nil {
		writeErr(w, 400, "保存失败，请检查渠道和生效时间是否重复")
		return
	}
	s.audit(r, "api_channel_rate_add", "api_channel", strconv.FormatInt(id, 10), map[string]any{"model": in.Model, "currency": in.Currency, "effective_at": in.EffectiveAt})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleProjectEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	rows, err := s.deps.Store.ProjectEvents(r.Context(), id, currentEmployee(r).ID)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"events": rows})
}
func (s *Server) handleCreateProjectEvent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var in store.ProjectEvent
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil {
		writeErr(w, 400, "内容无效或超过 2 MiB")
		return
	}
	if in.EmployeeID <= 0 || in.OccurredAt.IsZero() || strings.TrimSpace(in.Content) == "" {
		writeErr(w, 400, "请填写人员、时间和工作内容")
		return
	}
	private, err := s.deps.Store.PrivateWork(r.Context(), in.EmployeeID)
	if err != nil {
		writeErr(w, 400, "人员不存在")
		return
	}
	if private && in.EmployeeID != currentEmployee(r).ID {
		writeErr(w, 403, "无权为此人员添加记录")
		return
	}
	in.ProjectID = id
	out, err := s.deps.Store.CreateProjectEvent(r.Context(), in, currentEmployee(r).ID)
	if err != nil {
		writeErr(w, 400, "无法保存工作记录")
		return
	}
	s.audit(r, "project_event_create", "project", strconv.FormatInt(id, 10), map[string]any{"event_id": out.ID, "subject_employee_id": in.EmployeeID})
	writeJSON(w, 200, out)
}

func (s *Server) handleWorkReviews(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.WorkReviews(r.Context(), currentEmployee(r).ID)
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"reviews": rows})
}
func (s *Server) handleCreateWorkReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string `json:"kind"`
		From    string `json:"from"`
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil {
		writeErr(w, 400, "内容无效或超过 2 MiB")
		return
	}
	from, err1 := time.ParseInLocation(time.DateOnly, in.From, s.loc())
	to, err2 := time.ParseInLocation(time.DateOnly, in.To, s.loc())
	if (in.Kind != "monthly" && in.Kind != "quarterly") || err1 != nil || err2 != nil || to.Before(from) || strings.TrimSpace(in.Content) == "" {
		writeErr(w, 400, "请填写有效的复盘类型、时间和内容")
		return
	}
	review, err := s.deps.Store.CreateWorkReview(r.Context(), store.WorkReview{Kind: in.Kind, PeriodStart: from, PeriodEnd: to, Content: in.Content, CreatedBy: currentEmployee(r).ID})
	if err != nil {
		writeErr(w, 500, "保存失败")
		return
	}
	s.audit(r, "work_review_create", "work_review", strconv.FormatInt(review.ID, 10), map[string]any{"kind": in.Kind, "from": in.From, "to": in.To})
	writeJSON(w, 200, review)
}

func (s *Server) handleCaptureCoverage(w http.ResponseWriter, r *http.Request) {
	from, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("from"), s.loc())
	if err != nil {
		writeErr(w, 400, "请选择开始日期")
		return
	}
	last, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("to"), s.loc())
	if err != nil || last.Before(from) {
		writeErr(w, 400, "请选择有效的结束日期")
		return
	}
	rows, err := s.deps.Store.CaptureCoverage(r.Context(), from, last.AddDate(0, 0, 1))
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"rows": rows})
}
