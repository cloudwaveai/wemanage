package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aiseat/internal/health"
	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Settings.Get(r.Context()))
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in settings.Settings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if err := s.deps.Settings.Put(r.Context(), in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "settings_update", "", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListIPGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.deps.Store.ListIPGroups(r.Context(), s.deps.Box)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]any{
			"id": g.ID, "name": g.Name, "capacity": g.Capacity, "account_count": g.AccountCount,
			"expected_egress_ip": g.ExpectedEgressIP, "last_egress_ip": g.LastEgressIP,
			"last_check_error": g.LastCheckError, "last_checked_at": g.LastCheckedAt,
			"proxy_masked": maskProxy(g.ProxyURL),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip_groups": out})
}

func (s *Server) handleCreateIPGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ProxyURL string `json:"proxy_url"`
		Capacity int    `json:"capacity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.ProxyURL == "" {
		writeErr(w, http.StatusBadRequest, "name and proxy_url required")
		return
	}
	if err := health.ValidateProxy(body.ProxyURL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body.ProxyURL = strings.TrimSpace(body.ProxyURL)
	g, err := s.deps.Store.CreateIPGroup(r.Context(), s.deps.Box, body.Name, body.ProxyURL, body.Capacity)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	s.audit(r, "ip_group_create", "ip_group", fmt.Sprint(g.ID), map[string]any{"name": body.Name})
	writeJSON(w, http.StatusOK, map[string]any{"id": g.ID})
}

func (s *Server) handleUpdateIPGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Capacity int    `json:"capacity"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if body.ProxyURL = strings.TrimSpace(body.ProxyURL); body.ProxyURL != "" {
		if err := health.ValidateProxy(body.ProxyURL); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.deps.Store.UpdateIPGroup(r.Context(), s.deps.Box, id, body.Capacity, body.ProxyURL); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	s.audit(r, "ip_group_update", "ip_group", fmt.Sprint(id), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.deps.Store.ListAudit(r.Context(), store.AuditFilter{Limit: limit})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": rows})
}

func maskProxy(u string) string {
	if health.ProxyIsDirect(u) {
		return "direct"
	}
	if len(u) <= 12 {
		return "***"
	}
	return u[:8] + "***" + u[len(u)-4:]
}
