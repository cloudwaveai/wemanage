package web

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/feishu"
	"aiseat/internal/health"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
)

// Deps wires the web layer.
type Deps struct {
	Store            *store.Store
	Settings         *settings.Service
	Box              *secure.Box
	CPA              *cpa.Client
	Feishu           *feishu.Client
	FeishuLoginScope string
	PublicBaseURL    string
	SecureCookies    bool
	DevLogin         bool
	// Loc is the company time zone: charts bucket by its hours and days.
	Loc *time.Location
	// Models lists the models an account can use (optional).
	Models func(ctx context.Context, acc store.Account) ([]store.ModelInfo, error)
	// ModelCatalog lists a provider channel's models when no account is
	// bound yet (optional).
	ModelCatalog func(ctx context.Context, channel string) ([]store.ModelInfo, error)
	// Quota refreshes a ChatGPT account's 5-hour / weekly quota on demand.
	Quota *health.QuotaProber
	// BootstrapSuperAdmins are the open_ids pinned as super admin by config;
	// they are re-granted at every start, so the UI cannot revoke them.
	BootstrapSuperAdmins []string
	// SyncFeishu triggers a directory sync; wired by the serve command so the
	// web layer need not import the feishu sync logic directly.
	SyncFeishu func(ctx context.Context) (int, int, error)
	// Risk is the key-sharing engine (optional); used to refresh gate caches
	// and notify on device/freeze changes.
	Risk *risk.Engine
	Log  *slog.Logger
}

// Server holds web dependencies.
type Server struct {
	deps     Deps
	installs installCodes
}

// NewServer builds the web server.
func NewServer(deps Deps) *Server {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Server{deps: deps}
}

// Mount attaches the /api routes.
func (s *Server) Mount(r chi.Router) {
	// Install scripts: public, the single-use code is the credential.
	r.Get("/i/{code}", s.handleInstallScript)
	r.Route("/api", func(r chi.Router) {
		// Public auth endpoints.
		r.Get("/auth/feishu/login", s.handleFeishuLogin)
		r.Get("/auth/feishu/callback", s.handleFeishuCallback)
		if s.deps.DevLogin {
			r.Post("/auth/dev", s.handleDevLogin)
		}
		r.Get("/auth/options", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"dev_login": s.deps.DevLogin})
		})

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)

			// Self-service.
			r.Get("/me", s.handleMe)
			r.Post("/me/key", s.handleIssueKey)
			r.Get("/me/usage", s.handleMyUsage)
			r.Get("/me/quota", s.handleMyQuota)
			r.Get("/me/reports", s.handleMyReports)
			r.Post("/me/weekly-report", s.handleWeeklyReportPref)
			r.Post("/me/quota-alert", s.handleQuotaAlertPref)
			r.Get("/me/config-snippets", s.handleConfigSnippets)
			r.Post("/me/install-code", s.handleInstallCode)
			r.Get("/me/devices", s.handleMyDevices)
			r.Post("/me/devices/{id}/confirm", s.handleConfirmDevice)
			r.Post("/me/devices/{id}/deny", s.handleDenyDevice)

			// Team usage (department leaders see their departments; super admins everyone).
			r.Get("/team/usage", s.handleTeamUsage)

			// Logs (scoped by RBAC).
			r.Get("/logs", s.handleListLogs)
			r.Get("/logs/{id}", s.handleViewLog)

			// Admin (super-admin only).
			r.Group(func(r chi.Router) {
				r.Use(s.requireSuperAdmin)
				r.Get("/admin/overview", s.handleOverview)
				r.Get("/admin/employees", s.handleListEmployees)
				r.Get("/admin/accounts", s.handleListAccounts)
				r.Post("/admin/accounts/oauth-start", s.handleOAuthStart)
				r.Post("/admin/accounts/oauth-complete", s.handleOAuthComplete)
				r.Post("/admin/accounts/import", s.handleImportAccount)
				r.Post("/admin/accounts/kimi-start", s.handleKimiStart)
				r.Post("/admin/accounts/kimi-complete", s.handleKimiComplete)
				r.Post("/admin/accounts/{id}/bind", s.handleBind)
				r.Post("/admin/accounts/{id}/unbind", s.handleUnbind)
				r.Post("/admin/accounts/{id}/quota/refresh", s.handleRefreshQuota)
				r.Patch("/admin/accounts/{id}", s.handlePatchAccount)
				r.Get("/admin/ip-groups", s.handleListIPGroups)
				r.Post("/admin/ip-groups", s.handleCreateIPGroup)
				r.Patch("/admin/ip-groups/{id}", s.handleUpdateIPGroup)
				r.Get("/admin/settings", s.handleGetSettings)
				r.Put("/admin/settings", s.handlePutSettings)
				r.Post("/admin/sync-feishu", s.handleSyncFeishu)
				r.Get("/admin/audit", s.handleAudit)
				r.Get("/admin/risk/events", s.handleRiskEvents)
				r.Post("/admin/risk/events/{id}/resolve", s.handleResolveRiskEvent)
				r.Get("/admin/employees/{id}/devices", s.handleEmployeeDevices)
				r.Post("/admin/employees/{id}/freeze", s.handleFreezeEmployee(true))
				r.Post("/admin/employees/{id}/unfreeze", s.handleFreezeEmployee(false))
				r.Post("/admin/employees/{id}/remote", s.handleRemoteAllowed)
				r.Post("/admin/employees/{id}/super-admin", s.handleSuperAdmin)
				r.Post("/admin/devices/{id}/trust", s.handleAdminDevice("trust"))
				r.Post("/admin/devices/{id}/block", s.handleAdminDevice("block"))
				r.Post("/admin/devices/{id}/unblock", s.handleAdminDevice("unblock"))
			})
		})
	})
}

// audit is a convenience wrapper.
func (s *Server) audit(r *http.Request, action, targetType, targetID string, detail map[string]any) {
	emp := currentEmployee(r)
	var actor *int64
	if emp != nil {
		actor = &emp.ID
	}
	_ = s.deps.Store.Audit(r.Context(), store.AuditEntry{
		ActorID: actor, Action: action, TargetType: targetType, TargetID: targetID,
		Detail: detail, IP: clientIP(r),
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
