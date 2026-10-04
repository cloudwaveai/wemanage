package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Service accounts are callers that are not people, such as the company's New
// API platform calling the gateway with its own key. They are not in Feishu:
// administrators create them and issue their keys, and bind accounts to them
// like to anyone else.

func (s *Server) handleCreateServiceAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 50 {
		writeErr(w, http.StatusBadRequest, "名称不能为空，最多 50 个字")
		return
	}
	emp, err := s.deps.Store.CreateServiceAccount(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create")
		return
	}
	s.audit(r, "service_account_create", "employee", fmt.Sprint(emp.ID), map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]any{"id": emp.ID, "name": emp.Name})
}

// handleIssueServiceKey issues a service account's key, revoking the previous
// one. The plaintext is returned this once. People issue their own keys in
// the portal; only service accounts get theirs here.
func (s *Server) handleIssueServiceKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx := r.Context()
	emp, err := s.deps.Store.GetEmployee(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "employee not found")
		return
	}
	if !emp.IsService {
		writeErr(w, http.StatusBadRequest, "员工的 Key 由本人在门户生成；这里只能给服务账号生成")
		return
	}
	plain, key, err := s.deps.Store.IssueKey(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot issue key")
		return
	}
	s.audit(r, "service_key_issue", "employee", fmt.Sprint(id), map[string]any{"name": emp.Name, "prefix": key.KeyPrefix})
	writeJSON(w, http.StatusOK, map[string]any{"key": plain, "prefix": key.KeyPrefix})
}
