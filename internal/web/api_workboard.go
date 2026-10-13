package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/workdigest"
)

// The work board (super admins): every employee's token use over a period,
// and the daily LLM summaries of what each of them worked on.

func (s *Server) loc() *time.Location {
	if s.deps.Loc != nil {
		return s.deps.Loc
	}
	return time.Local
}

func midnight(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// workRange maps ?range= (today, yesterday, 7d, 30d) to [from,to) and the
// number of calendar days it spans.
func workRange(r *http.Request, loc *time.Location) (from, to time.Time, days int) {
	now := time.Now().In(loc)
	today := midnight(now, loc)
	switch r.URL.Query().Get("range") {
	case "today":
		return today, now, 1
	case "yesterday":
		return today.AddDate(0, 0, -1), today, 1
	case "30d":
		return today.AddDate(0, 0, -29), now, 30
	default:
		return today.AddDate(0, 0, -6), now, 7
	}
}

func (s *Server) handleWorkboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := s.loc()
	from, to, days := workRange(r, loc)

	usage, err := s.deps.Store.Usage(ctx, from, to, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	work, err := s.deps.Store.WorkByEmployee(ctx, from, to, loc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	emps, err := s.deps.Store.ListEmployees(ctx, store.EmployeeFilter{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "employee query failed")
		return
	}

	rows := make([]map[string]any, 0, len(emps))
	var idle []map[string]any
	for _, e := range emps {
		u, used := work[e.ID]
		if !used {
			if e.Status == "active" && !e.IsService {
				idle = append(idle, map[string]any{"employee_id": e.ID, "name": e.Name})
			}
			continue
		}
		rows = append(rows, map[string]any{
			"employee_id": e.ID, "name": e.Name, "department_names": nonNil(e.DepartmentNames),
			"is_service": e.IsService, "status": e.Status,
			"requests": u.Requests, "tokens_input": u.TokensInput, "tokens_cached": u.TokensCached,
			"tokens_output": u.TokensOutput, "tokens_reasoning": u.TokensReasoning,
			"tokens_total": u.TokensInput + u.TokensOutput, "sessions": u.Sessions,
			"active_days": u.ActiveDays, "last_active": u.LastActive, "top_model": u.TopModel,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i]["tokens_total"].(int64) > rows[j]["tokens_total"].(int64)
	})

	out := map[string]any{
		"from": from, "to": to, "days": days,
		"totals": map[string]any{
			"requests": usage.Requests, "tokens_input": usage.TokensInput, "tokens_cached": usage.TokensCached,
			"tokens_output": usage.TokensOutput, "tokens_reasoning": usage.TokensReasoning,
			"tokens_total": usage.TokensInput + usage.TokensOutput, "active_employees": len(rows),
		},
		"employees": rows,
		"idle":      nonNil(idle),
		"by_model":  nonNil(usage.ByModel),
		"daily":     []store.SeriesPoint{},
	}
	if days > 1 {
		series, err := s.deps.Store.UsageSeries(ctx, from, to, nil, 24*time.Hour, midnight(to, loc))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "usage query failed")
			return
		}
		out["daily"] = series
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleWorkDigests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := s.loc()
	recent, err := s.deps.Store.WorkDigestDays(ctx, 60)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	var day time.Time
	if q := r.URL.Query().Get("day"); q != "" {
		day, err = time.ParseInLocation(time.DateOnly, q, loc)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "day must be YYYY-MM-DD")
			return
		}
	} else if len(recent) > 0 {
		day = time.Date(recent[0].Year(), recent[0].Month(), recent[0].Day(), 0, 0, 0, 0, loc)
	} else {
		day = midnight(time.Now(), loc).AddDate(0, 0, -1)
	}

	set := s.deps.Settings.Get(ctx)
	out := map[string]any{"day": day.Format(time.DateOnly), "status": "none", "configured": false}
	if set.WorkDigestAccountID != 0 {
		out["configured"] = true
		model := set.WorkDigestModel
		if model == "" {
			model = set.DefaultCodexModel
		}
		out["model"] = model
		if acc, err := s.deps.Store.GetAccount(ctx, set.WorkDigestAccountID); err == nil {
			out["account"] = map[string]any{"id": acc.ID, "email": acc.Email, "plan_type": acc.PlanType}
		}
	}
	days := make([]string, 0, len(recent))
	for _, d := range recent {
		days = append(days, d.Format(time.DateOnly))
	}
	out["days"] = days

	if d, err := s.deps.Store.GetWorkDigestDay(ctx, day); err == nil {
		out["status"], out["error"] = d.Status, d.Error
		out["overview_md"], out["started_at"], out["finished_at"] = d.OverviewMD, d.StartedAt, d.FinishedAt
		running := s.deps.Digest != nil && s.deps.Digest.Running()
		if d.Status == "running" && !running {
			out["status"], out["error"] = "failed", "上次生成被中断了（服务重启），请重新生成。"
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}

	digests, err := s.deps.Store.WorkDigests(ctx, day)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	names := map[int64]store.EmployeeRow{}
	if emps, err := s.deps.Store.ListEmployees(ctx, store.EmployeeFilter{}); err == nil {
		for _, e := range emps {
			names[e.ID] = e
		}
	}
	list := make([]map[string]any, 0, len(digests))
	for _, d := range digests {
		e := names[d.EmployeeID]
		list = append(list, map[string]any{
			"employee_id": d.EmployeeID, "name": e.Name, "department_names": nonNil(e.DepartmentNames),
			"summary_md": d.SummaryMD, "sessions": d.Sessions, "messages": d.Messages, "generated_at": d.GeneratedAt,
		})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i]["name"].(string) < list[j]["name"].(string) })
	out["employees"] = list
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunWorkDigest(w http.ResponseWriter, r *http.Request) {
	if s.deps.Digest == nil {
		writeErr(w, http.StatusServiceUnavailable, "work digests are not available")
		return
	}
	var in struct {
		Day string `json:"day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	loc := s.loc()
	day, err := time.ParseInLocation(time.DateOnly, in.Day, loc)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "day must be YYYY-MM-DD")
		return
	}
	if day.After(time.Now()) {
		writeErr(w, http.StatusBadRequest, "这一天还没到")
		return
	}
	if err := s.deps.Digest.Start(day); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, workdigest.ErrBusy) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	s.audit(r, "work_digest_run", "day", in.Day, nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
