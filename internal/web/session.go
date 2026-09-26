// Package web serves the management API and employee self-service API, with
// Feishu-based login, cookie sessions and CSRF protection.
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"aiseat/internal/store"
)

const (
	sessionCookie = "aiseat_sess"
	csrfHeader    = "X-CSRF-Token"
	sessionTTL    = 7 * 24 * time.Hour
)

type ctxKey int

const (
	empKey  ctxKey = 1
	csrfKey ctxKey = 2
)

// currentCSRF returns the session CSRF token from context.
func currentCSRF(r *http.Request) string {
	s, _ := r.Context().Value(csrfKey).(string)
	return s
}

// currentEmployee returns the authenticated employee from the request context.
func currentEmployee(r *http.Request) *store.Employee {
	e, _ := r.Context().Value(empKey).(*store.Employee)
	return e
}

// setSessionCookie writes the session cookie.
func setSessionCookie(w http.ResponseWriter, id string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// requireAuth loads the session's employee into context, or returns 401.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		ws, err := s.deps.Store.GetWebSession(r.Context(), c.Value)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session expired")
			return
		}
		emp, err := s.deps.Store.GetEmployee(r.Context(), ws.EmployeeID)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unknown employee")
			return
		}
		// CSRF for state-changing methods.
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if r.Header.Get(csrfHeader) != ws.CSRFToken {
				writeErr(w, http.StatusForbidden, "bad or missing CSRF token")
				return
			}
		}
		ctx := context.WithValue(r.Context(), empKey, &emp)
		ctx = context.WithValue(ctx, csrfKey, ws.CSRFToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSuperAdmin gates super-admin-only endpoints.
func (s *Server) requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		emp := currentEmployee(r)
		if emp == nil || !emp.IsSuperAdmin {
			writeErr(w, http.StatusForbidden, "super admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
