package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

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
	// Links in Feishu messages point at a page (e.g. /devices); come back to it.
	if next := r.URL.Query().Get("next"); safeNext(next) {
		http.SetCookie(w, &http.Cookie{Name: nextCookie, Value: url.QueryEscape(next), Path: "/api/auth",
			HttpOnly: true, Secure: s.deps.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	}
	redirect := s.deps.PublicBaseURL + "/api/auth/feishu/callback"
	http.Redirect(w, r, s.deps.Feishu.OAuthURL(state, redirect, s.deps.FeishuLoginScope), http.StatusFound)
}

const nextCookie = "aiseat_next"

// safeNext accepts only a path on this site.
func safeNext(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/\\") &&
		!strings.HasPrefix(p, "/api/") && !strings.ContainsAny(p, "\r\n")
}

// loginFailed sends the browser back to the portal, which shows why: the
// login page may be inside Feishu, where a bare JSON error helps no one.
func loginFailed(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?login_error="+url.QueryEscape(msg), http.StatusFound)
}

func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	if s.deps.Feishu == nil {
		writeErr(w, http.StatusServiceUnavailable, "feishu login not configured")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		loginFailed(w, r, "飞书没有返回登录信息，请重试。")
		return
	}
	// Validate and consume state.
	tag, err := s.deps.Store.Pool.Exec(r.Context(), `DELETE FROM oauth_states WHERE state=$1`, state)
	if err != nil || tag.RowsAffected() == 0 {
		loginFailed(w, r, "登录链接已过期，请重试。")
		return
	}
	redirect := s.deps.PublicBaseURL + "/api/auth/feishu/callback"
	info, err := s.deps.Feishu.ExchangeUser(r.Context(), code, redirect)
	if err != nil {
		s.deps.Log.Warn("feishu login: code exchange failed", "err", err)
		loginFailed(w, r, "飞书登录失败，请重试。")
		return
	}
	emp, err := s.deps.Store.GetEmployeeByOpenID(r.Context(), info.OpenID)
	if err != nil {
		// Logged so the first administrator can find their open_id for
		// BOOTSTRAP_SUPER_ADMINS before the directory has been synced.
		s.deps.Log.Info("feishu login rejected: not in the employee list", "open_id", info.OpenID, "name", info.Name)
		loginFailed(w, r, "账号还没开通，请联系管理员同步通讯录。")
		return
	}
	if emp.Status != "active" {
		loginFailed(w, r, "账号已停用，如有疑问请联系管理员。")
		return
	}
	s.startSession(w, r.Context(), emp.ID)
	dest := "/"
	if c, err := r.Cookie(nextCookie); err == nil {
		if next, err := url.QueryUnescape(c.Value); err == nil && safeNext(next) {
			dest = next
		}
		http.SetCookie(w, &http.Cookie{Name: nextCookie, Path: "/api/auth", MaxAge: -1})
	}
	http.Redirect(w, r, dest, http.StatusFound)
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
