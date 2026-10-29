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

// handleIssueServiceKey issues an additional key for a service account.
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
	var body struct {
		Label     string `json:"label"`
		ChannelID *int64 `json:"channel_id"`
		ProjectID *int64 `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	body.Label = strings.TrimSpace(body.Label)
	if len([]rune(body.Label)) > 50 {
		writeErr(w, http.StatusBadRequest, "名称最多 50 个字")
		return
	}
	if body.ChannelID != nil {
		channel, err := s.deps.Store.GetAPIChannel(ctx, *body.ChannelID)
		if err != nil || !channel.Active {
			writeErr(w, http.StatusBadRequest, "上游渠道不可用")
			return
		}
	}
	plain, key, err := s.deps.Store.IssueServiceKey(ctx, id, body.Label, body.ChannelID, body.ProjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot issue key")
		return
	}
	s.audit(r, "service_key_issue", "employee", fmt.Sprint(id), map[string]any{"name": emp.Name, "prefix": key.Prefix})
	writeJSON(w, http.StatusOK, map[string]any{"key": plain, "prefix": key.Prefix, "id": key.ID})
}

func (s *Server) handleServiceKeys(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	emp, err := s.deps.Store.GetEmployee(r.Context(), id)
	if err != nil || !emp.IsService {
		writeErr(w, http.StatusNotFound, "service account not found")
		return
	}
	keys, err := s.deps.Store.ListServiceKeys(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleRevokeServiceKey(w http.ResponseWriter, r *http.Request) {
	id, e1 := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	keyID, e2 := strconv.ParseInt(chi.URLParam(r, "keyID"), 10, 64)
	if e1 != nil || e2 != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	emp, err := s.deps.Store.GetEmployee(r.Context(), id)
	if err != nil || !emp.IsService {
		writeErr(w, http.StatusNotFound, "service account not found")
		return
	}
	ok, err := s.deps.Store.RevokeServiceKey(r.Context(), id, keyID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot revoke")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "key not found")
		return
	}
	s.audit(r, "service_key_revoke", "employee", fmt.Sprint(id), map[string]any{"key_id": keyID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
