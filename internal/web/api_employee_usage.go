package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleEmployeeWorkUsage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的员工编号")
		return
	}
	emp, err := s.deps.Store.GetEmployee(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	private, err := s.deps.Store.PrivateWork(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if private && currentEmployee(r).ID != id {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	loc := s.loc()
	from, to, days := workRange(r, loc)
	if day := r.URL.Query().Get("day"); day != "" {
		from, err = time.ParseInLocation(time.DateOnly, day, loc)
		if err != nil || from.After(midnight(time.Now(), loc)) {
			writeErr(w, http.StatusBadRequest, "请选择有效的日期")
			return
		}
		to = from.AddDate(0, 0, 1)
		if now := time.Now(); to.After(now) {
			to = now
		}
		days = 1
	}
	usage, err := s.deps.Store.Usage(r.Context(), from, to, []int64{id})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	series, err := s.deps.Store.UsageSeries(r.Context(), from, to, []int64{id}, time.Hour, midnight(from, loc))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "series query failed")
		return
	}
	sessions, imported, err := s.deps.Store.EmployeeUsageCounts(r.Context(), id, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"employee_id": id, "name": emp.Name, "is_service": emp.IsService, "from": from, "to": to, "days": days, "timezone": loc.String(),
		"requests": usage.Requests, "sessions": sessions, "imported_records": imported,
		"tokens_input": usage.TokensInput, "tokens_cached": usage.TokensCached, "tokens_output": usage.TokensOutput,
		"tokens_reasoning": usage.TokensReasoning, "tokens_total": usage.TokensInput + usage.TokensOutput,
		"by_model": nonNil(usage.ByModel), "hourly": nonNil(series),
	})
}
