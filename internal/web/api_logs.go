package web

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	sc, err := s.computeScope(r.Context(), emp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "scope error")
		return
	}
	q := r.URL.Query()
	f := store.RequestFilter{
		Query:  q.Get("q"),
		Client: q.Get("client"),
		Model:  q.Get("model"),
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = t
		}
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))

	// RBAC: restrict to the visible employee set.
	if reqEmp := q.Get("employee_id"); reqEmp != "" {
		id, _ := strconv.ParseInt(reqEmp, 10, 64)
		if !sc.CanViewEmployee(id) {
			writeErr(w, http.StatusForbidden, "无权查看该员工")
			return
		}
		f.EmployeeIDs = []int64{id}
	} else if !sc.IsSuperAdmin {
		f.EmployeeIDs = sc.VisibleEmployeeIDs()
	}

	items, total, err := s.deps.Store.ListRequests(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"id": hex.EncodeToString(it.ID[:]), "started_at": it.StartedAt,
			"employee_id": it.EmployeeID, "employee_name": it.EmployeeName,
			"client_kind": it.ClientKind, "endpoint": it.Endpoint,
			"model_requested": it.ModelRequested, "model_responded": it.ModelResponded,
			"status_code": it.StatusCode, "tokens_input": it.TokensInput,
			"tokens_output": it.TokensOutput, "duration_ms": it.DurationMS,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out, "total": total})
}

func (s *Server) handleViewLog(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	idHex := chi.URLParam(r, "id")
	idb, err := hex.DecodeString(idHex)
	if err != nil || len(idb) != 16 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var id [16]byte
	copy(id[:], idb)

	row, err := s.deps.Store.GetRequestFull(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	sc, err := s.computeScope(r.Context(), emp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "scope error")
		return
	}
	if !sc.CanViewEmployee(row.EmployeeID) {
		writeErr(w, http.StatusForbidden, "无权查看该员工的日志")
		return
	}
	// Every transcript view is audited (who viewed whom).
	s.audit(r, "view_transcript", "request", idHex, map[string]any{"subject_employee_id": row.EmployeeID})

	tr, err := s.reconstruct(r.Context(), row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reconstruct failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              idHex,
		"started_at":      row.StartedAt,
		"employee_id":     row.EmployeeID,
		"client_kind":     row.ClientKind,
		"endpoint":        row.Endpoint,
		"model_requested": row.ModelRequested,
		"model_responded": row.ModelResponded,
		"status_code":     row.StatusCode,
		"tokens":          map[string]int64{"input": row.TokensInput, "cached": row.TokensCached, "output": row.TokensOutput, "reasoning": row.TokensReasoning},
		"transcript":      tr,
	})
}
