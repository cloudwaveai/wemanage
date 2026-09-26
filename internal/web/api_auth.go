package web

import (
	"context"
	"encoding/json"
	"net/http"

	"aiseat/internal/secure"
	"aiseat/internal/store"
)

func (s *Server) handleFeishuLogin(w http.ResponseWriter, r *http.Request) {
	if s.deps.Feishu == nil {
		writeErr(w, http.StatusServiceUnavailable, "feishu login not configured")
		return
	}
	state := secure.RandomToken(24)
	if _, err := s.deps.Store.Pool.Exec(r.Context(),
		`INSERT INTO oauth_states (state) VALUES ($1)`, state); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot start login")
		return
	}
	redirect := s.deps.PublicBaseURL + "/api/auth/feishu/callback"
	http.Redirect(w, r, s.deps.Feishu.OAuthURL(state, redirect), http.StatusFound)
}

func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	if s.deps.Feishu == nil {
		writeErr(w, http.StatusServiceUnavailable, "feishu login not configured")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeErr(w, http.StatusBadRequest, "missing code or state")
		return
	}
	// Validate and consume state.
	tag, err := s.deps.Store.Pool.Exec(r.Context(), `DELETE FROM oauth_states WHERE state=$1`, state)
	if err != nil || tag.RowsAffected() == 0 {
		writeErr(w, http.StatusBadRequest, "invalid or expired state")
		return
	}
	redirect := s.deps.PublicBaseURL + "/api/auth/feishu/callback"
	info, err := s.deps.Feishu.ExchangeUser(r.Context(), code, redirect)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "feishu exchange failed")
		return
	}
	emp, err := s.deps.Store.GetEmployeeByOpenID(r.Context(), info.OpenID)
	if err != nil {
		writeErr(w, http.StatusForbidden, "账号未开通，请联系管理员先同步通讯录")
		return
	}
	if emp.Status != "active" {
		writeErr(w, http.StatusForbidden, "账号已停用")
		return
	}
	s.startSession(w, r.Context(), emp.ID)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OpenID string `json:"open_id"`
		Name   string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.OpenID == "" {
		writeErr(w, http.StatusBadRequest, "open_id required")
		return
	}
	emp, err := s.deps.Store.GetEmployeeByOpenID(r.Context(), body.OpenID)
	if err != nil {
		emp, err = s.deps.Store.UpsertEmployeeByOpenID(r.Context(), store.Employee{FeishuOpenID: body.OpenID, Name: firstNonEmpty(body.Name, body.OpenID)})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot create dev user")
			return
		}
	}
	csrf := s.startSession(w, r.Context(), emp.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "employee_id": emp.ID, "csrf": csrf})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.deps.Store.DeleteWebSession(r.Context(), c.Value)
	}
	clearSessionCookie(w, s.deps.SecureCookies)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) startSession(w http.ResponseWriter, ctx context.Context, employeeID int64) string {
	id, csrf, err := s.deps.Store.CreateWebSession(ctx, employeeID, sessionTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create session")
		return ""
	}
	setSessionCookie(w, id, s.deps.SecureCookies)
	return csrf
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
