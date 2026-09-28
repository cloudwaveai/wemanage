package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/secure"
	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	to := time.Now()
	from := to.Add(-30 * 24 * time.Hour)
	usage, _ := s.deps.Store.Usage(ctx, from, to, nil)
	active, _ := s.deps.Store.ActiveEmployeeCount(ctx, to.Add(-24*time.Hour), to)
	accounts, _ := s.deps.Store.ListAccounts(ctx)

	health := map[string]int{}
	var alerts []map[string]any
	for _, a := range accounts {
		st := accountStatus(a.Account)
		health[st]++
		if sigs, _ := s.deps.Store.OpenSignals(ctx, a.ID); len(sigs) > 0 {
			for _, sig := range sigs {
				alerts = append(alerts, map[string]any{
					"account_id": a.ID, "account_email": a.Email, "kind": sig.Kind, "at": sig.At,
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"usage_30d":      usage,
		"active_24h":     active,
		"account_health": health,
		"account_total":  len(accounts),
		"alerts":         alerts,
	})
}

func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListEmployees(r.Context(), store.EmployeeFilter{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		out = append(out, map[string]any{
			"id": e.ID, "name": e.Name, "email": e.Email, "status": e.Status,
			"is_super_admin": e.IsSuperAdmin, "notice_acked": e.NoticeAckAt != nil,
			"account_id": e.AccountID, "account_email": e.AccountEmail, "account_status": e.AccountStatus,
			"active_key_prefix": e.ActiveKeyPrefix, "department_names": e.DepartmentNames,
			"frozen": e.FrozenAt != nil, "frozen_reason": e.FrozenReason, "open_risk_events": e.OpenRiskEvents,
			"remote_allowed": e.RemoteAllowed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": out})
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Enrich with computed status.
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		out = append(out, map[string]any{
			"id": a.ID, "email": a.Email, "plan_type": a.PlanType, "owner_type": a.OwnerType,
			"employee_id": a.EmployeeID, "employee_name": a.EmployeeName,
			"ip_group_id": a.IPGroupID, "ip_group_name": a.IPGroupName,
			"status": accountStatus(a.Account), "needs_login": a.NeedsLogin,
			"rate_limited_until": a.RateLimitedUntil, "max_concurrency": a.MaxConcurrency,
			"device_profile": a.DeviceProfile, "cpa_drift": a.CPADrift,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id required")
		return
	}
	authURL, state, err := s.deps.CPA.RequestCodexAuthURL(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot start CPA OAuth: "+err.Error())
		return
	}
	known, _ := s.deps.CPA.ListAuthFiles(r.Context())
	names := make([]string, 0, len(known))
	for _, f := range known {
		names = append(names, f.Name)
	}
	emp := currentEmployee(r)
	if err := s.deps.Store.CreateOnboarding(r.Context(), store.Onboarding{
		State: state, IPGroupID: body.IPGroupID, OSProfile: body.OS, KnownAuthNames: names, CreatedBy: &emp.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot record onboarding")
		return
	}
	s.audit(r, "account_oauth_start", "ip_group", fmt.Sprint(body.IPGroupID), nil)
	writeJSON(w, http.StatusOK, map[string]any{"auth_url": authURL, "state": state})
}

func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		State       string `json:"state"`
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.State == "" || body.RedirectURL == "" {
		writeErr(w, http.StatusBadRequest, "state and redirect_url required")
		return
	}
	ob, err := s.deps.Store.GetOnboarding(r.Context(), body.State)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unknown onboarding state")
		return
	}
	if err := s.deps.CPA.PostOAuthCallback(r.Context(), "codex", body.RedirectURL); err != nil {
		writeErr(w, http.StatusBadGateway, "callback failed: "+err.Error())
		return
	}
	// Poll for completion.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := s.deps.CPA.AuthStatus(r.Context(), body.State); st == "ok" {
			break
		}
		time.Sleep(time.Second)
	}
	// Find the newly-added codex auth file.
	files, err := s.deps.CPA.ListAuthFiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot list auth files")
		return
	}
	known := map[string]bool{}
	for _, n := range ob.KnownAuthNames {
		known[n] = true
	}
	var newFile *cpa.AuthFile
	for i := range files {
		if files[i].Type == "codex" && !known[files[i].Name] {
			newFile = &files[i]
			break
		}
	}
	if newFile == nil {
		writeErr(w, http.StatusBadGateway, "no new codex account detected; check the pasted URL")
		return
	}
	acc, err := s.finishAccount(r, ob, newFile.Name, newFile.Email)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.CompleteOnboarding(r.Context(), body.State, acc.ID)
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"email": acc.Email, "via": "oauth"})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "email": acc.Email})
}

func (s *Server) handleImportAccount(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
		Content   json.RawMessage     `json:"content"` // the auth JSON
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 || len(body.Content) == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id and content required")
		return
	}
	name := "codex-" + secure.RandomToken(8) + ".json"
	if err := s.deps.CPA.UploadAuthFile(r.Context(), name, body.Content); err != nil {
		writeErr(w, http.StatusBadGateway, "upload failed: "+err.Error())
		return
	}
	ob := store.Onboarding{IPGroupID: body.IPGroupID, OSProfile: body.OS}
	acc, err := s.finishAccount(r, ob, name, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"via": "import"})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID})
}

// finishAccount creates the account row, applies device profile and pushes
// prefix+proxy to CPA.
func (s *Server) finishAccount(r *http.Request, ob store.Onboarding, cpaName, email string) (store.Account, error) {
	set := s.deps.Settings.Get(r.Context())
	dp := ob.OSProfile
	if dp.OS == "" {
		dp = set.DefaultDeviceProfile
	}
	dp.InstallationID = uuid.NewString()
	acc, err := s.deps.Store.CreateAccount(r.Context(), store.Account{
		CPAAuthName: cpaName, Email: email, IPGroupID: ob.IPGroupID, DeviceProfile: dp,
	})
	if err != nil {
		return store.Account{}, fmt.Errorf("create account: %w", err)
	}
	grp, err := s.deps.Store.GetIPGroup(r.Context(), s.deps.Box, ob.IPGroupID)
	if err == nil && s.deps.CPA != nil {
		if err := cpa.ApplyExpected(r.Context(), s.deps.CPA, acc, acc.Prefix(), grp.ProxyURL); err != nil {
			_ = s.deps.Store.SetCPADrift(r.Context(), acc.ID, "apply on create failed: "+err.Error())
		}
	}
	return acc, nil
}

func (s *Server) handleBind(w http.ResponseWriter, r *http.Request) {
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		EmployeeID int64 `json:"employee_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EmployeeID == 0 {
		writeErr(w, http.StatusBadRequest, "employee_id required")
		return
	}
	if err := s.deps.Store.BindAccount(r.Context(), accID, body.EmployeeID); err != nil {
		if err == store.ErrConflict {
			writeErr(w, http.StatusConflict, "该账号或该员工已被绑定")
			return
		}
		writeErr(w, http.StatusInternalServerError, "bind failed")
		return
	}
	s.audit(r, "account_bind", "account", fmt.Sprint(accID), map[string]any{"employee_id": body.EmployeeID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUnbind(w http.ResponseWriter, r *http.Request) {
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.deps.Store.UnbindAccount(r.Context(), accID); err != nil {
		writeErr(w, http.StatusInternalServerError, "unbind failed")
		return
	}
	s.audit(r, "account_unbind", "account", fmt.Sprint(accID), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePatchAccount(w http.ResponseWriter, r *http.Request) {
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var p store.AccountPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	// If IP group changes, re-apply prefix+proxy to CPA and audit the risk.
	if err := s.deps.Store.UpdateAccount(r.Context(), accID, p); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	if p.IPGroupID != nil {
		if acc, err := s.deps.Store.GetAccount(r.Context(), accID); err == nil {
			if grp, err := s.deps.Store.GetIPGroup(r.Context(), s.deps.Box, *p.IPGroupID); err == nil && s.deps.CPA != nil {
				_ = cpa.ApplyExpected(r.Context(), s.deps.CPA, acc, acc.Prefix(), grp.ProxyURL)
			}
		}
		s.audit(r, "account_change_ip_group", "account", fmt.Sprint(accID), map[string]any{"ip_group_id": *p.IPGroupID, "risk": "changing egress IP is itself a risk signal"})
	} else {
		s.audit(r, "account_update", "account", fmt.Sprint(accID), nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSyncFeishu(w http.ResponseWriter, r *http.Request) {
	if s.deps.Feishu == nil {
		writeErr(w, http.StatusServiceUnavailable, "feishu not configured")
		return
	}
	// The sync itself lives in the feishu package; call it via the injected runner.
	if s.deps.SyncFeishu == nil {
		writeErr(w, http.StatusServiceUnavailable, "sync not wired")
		return
	}
	d, u, err := s.deps.SyncFeishu(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sync failed: "+err.Error())
		return
	}
	s.audit(r, "feishu_sync", "", "", map[string]any{"departments": d, "users": u})
	writeJSON(w, http.StatusOK, map[string]any{"departments": d, "users": u})
}
