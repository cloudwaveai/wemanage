package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/health"
	"aiseat/internal/secure"
	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
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
			"is_super_admin": e.IsSuperAdmin, "is_service": e.IsService,
			"accounts":          employeeAccountsJSON(e.Accounts),
			"active_key_prefix": e.ActiveKeyPrefix, "department_names": e.DepartmentNames,
			"frozen": e.FrozenAt != nil, "frozen_reason": e.FrozenReason, "open_risk_events": e.OpenRiskEvents,
			"remote_allowed": e.RemoteAllowed, "super_admin_pinned": s.pinnedSuperAdmin(e.FeishuOpenID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": out})
}

// pinnedSuperAdmin reports whether config (BOOTSTRAP_SUPER_ADMINS) pins openID.
func (s *Server) pinnedSuperAdmin(openID string) bool {
	return openID != "" && slices.Contains(s.deps.BootstrapSuperAdmins, openID)
}

// handleSuperAdmin grants or revokes super admin. Nobody can change their own
// role, so at least one super admin always remains; admins pinned by config
// are re-granted at every start and must be removed from
// BOOTSTRAP_SUPER_ADMINS before they can be revoked here.
func (s *Server) handleSuperAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Value bool `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if me := currentEmployee(r); me != nil && me.ID == id {
		writeErr(w, http.StatusBadRequest, "cannot change your own super admin role")
		return
	}
	ctx := r.Context()
	emp, err := s.deps.Store.GetEmployee(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "employee not found")
		return
	}
	if body.Value && emp.Status != "active" {
		writeErr(w, http.StatusBadRequest, "employee is not active")
		return
	}
	if !body.Value && s.pinnedSuperAdmin(emp.FeishuOpenID) {
		writeErr(w, http.StatusBadRequest, "pinned by BOOTSTRAP_SUPER_ADMINS; remove it from the server config first")
		return
	}
	if err := s.deps.Store.SetSuperAdmin(ctx, id, body.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update")
		return
	}
	action := "super_admin_revoke"
	if body.Value {
		action = "super_admin_grant"
	}
	s.audit(r, action, "employee", fmt.Sprint(id), map[string]any{"name": emp.Name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "is_super_admin": body.Value})
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.ListAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Enrich with computed status, the latest quota snapshot and gateway usage.
	// Both extras are best effort: the list still renders without them.
	ctx := r.Context()
	ids := make([]int64, len(rows))
	for i, a := range rows {
		ids[i] = a.ID
	}
	quotas, err := s.deps.Store.LatestQuotas(ctx, ids)
	if err != nil {
		s.deps.Log.Warn("account list: quotas", "err", err)
	}
	usage, err := s.deps.Store.UsageByAccount(ctx)
	if err != nil {
		s.deps.Log.Warn("account list: usage", "err", err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		members := a.Members
		if members == nil {
			members = []store.AccountMember{}
		}
		m := map[string]any{
			"id": a.ID, "email": a.Email, "plan_type": a.PlanType, "owner_type": a.OwnerType,
			"members":     members,
			"ip_group_id": a.IPGroupID, "ip_group_name": a.IPGroupName,
			"status": accountStatus(a.Account), "needs_login": a.NeedsLogin, "provider": a.Provider,
			"rate_limited_until": a.RateLimitedUntil, "max_concurrency": a.MaxConcurrency,
			"device_profile": a.DeviceProfile, "cpa_drift": a.CPADrift,
			"allowed_models": a.AllowedModels, "allow_third_party": a.AllowThirdParty,
		}
		if q, ok := quotas[a.ID]; ok {
			m["quota"] = quotaJSON(q)
		}
		u := usage[a.ID]
		m["usage"] = map[string]any{
			"requests_24h": u.Requests24h, "tokens_24h": u.Tokens24h,
			"requests_7d": u.Requests7d, "tokens_7d": u.Tokens7d,
			"requests_all": u.RequestsAll, "tokens_all": u.TokensAll,
			"last_active": u.LastActive,
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out,
		"max_members": s.deps.Settings.Get(ctx).MaxAccountMembers})
}

// employeeAccountsJSON lists an employee's accounts with their status.
func employeeAccountsJSON(accs []store.EmployeeAccount) []map[string]any {
	out := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		out = append(out, map[string]any{
			"id": a.ID, "email": a.Email, "provider": a.Provider, "plan_type": a.PlanType,
			"status": accountStatus(store.Account{Status: a.Status, NeedsLogin: a.NeedsLogin, RateLimitedUntil: a.RateLimitedUntil}),
		})
	}
	return out
}

// handleRefreshQuota asks ChatGPT for one account's current quota now.
func (s *Server) handleRefreshQuota(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if s.deps.Quota == nil {
		writeErr(w, http.StatusServiceUnavailable, "quota refresh not configured")
		return
	}
	q, err := s.deps.Quota.Refresh(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "account not found")
		return
	case errors.Is(err, health.ErrRefreshTooSoon):
		writeErr(w, http.StatusTooManyRequests, "刚刷新过，请一分钟后再试")
		return
	case err != nil:
		s.deps.Log.Warn("quota refresh", "account", id, "err", err)
		writeErr(w, http.StatusBadGateway, "查询额度失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota": quotaJSON(q)})
}

// quotaJSON renders a quota snapshot: primary is the 5-hour window, secondary
// the weekly one (as reported by ChatGPT).
func quotaJSON(q store.QuotaSnapshot) map[string]any {
	return map[string]any{
		"at":                       q.At,
		"primary_used_pct":         q.PrimaryUsedPct,
		"primary_window_minutes":   q.PrimaryWindowMinutes,
		"primary_reset_at":         q.PrimaryResetAt,
		"secondary_used_pct":       q.SecondaryUsedPct,
		"secondary_window_minutes": q.SecondaryWindowMinutes,
		"secondary_reset_at":       q.SecondaryResetAt,
	}
}

// handleOAuthStart and handleOAuthComplete are the ChatGPT login (login.go).
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id required")
		return
	}
	s.startLogin(w, r, store.ProviderCodex, body.IPGroupID, body.OS)
}

func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	s.handleLoginComplete(w, r)
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
	provider := gjson.GetBytes(body.Content, "type").String()
	if provider == "" {
		provider = store.ProviderCodex
	}
	name := provider + "-" + secure.RandomToken(8) + ".json"
	if err := s.deps.CPA.UploadAuthFile(r.Context(), name, body.Content); err != nil {
		writeErr(w, http.StatusBadGateway, "upload failed: "+err.Error())
		return
	}
	ob := store.Onboarding{IPGroupID: body.IPGroupID, OSProfile: body.OS}
	acc, err := s.finishAccount(r, ob, name, "", provider)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"via": "import"})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID})
}

// handleDeleteAccount removes an account from the gateway and its credential
// from CPA, freeing its place in the IP group. The upstream account itself is
// untouched and can be logged in again later.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	acc, err := s.deps.Store.GetAccount(ctx, accID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	memberIDs, _ := s.deps.Store.AccountMemberIDs(ctx, accID)
	if err := s.deps.Store.DeleteAccount(ctx, accID); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed: "+err.Error())
		return
	}
	warning := ""
	if s.deps.CPA != nil && acc.CPAAuthName != "" {
		if err := s.deps.CPA.DeleteAuthFile(ctx, acc.CPAAuthName); err != nil {
			warning = "已从网关移除，但 CPA 里的凭证没删掉（" + err.Error() + "），可以在 CPA 管理页手动删除 " + acc.CPAAuthName
		}
	}
	s.audit(r, "account_delete", "account", fmt.Sprint(accID), map[string]any{
		"email": acc.Email, "provider": acc.Provider, "ip_group_id": acc.IPGroupID, "cpa_name": acc.CPAAuthName, "member_ids": memberIDs,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": warning})
}

// finishAccount creates the account row, applies device profile and pushes
// prefix+proxy to CPA.
func (s *Server) finishAccount(r *http.Request, ob store.Onboarding, cpaName, email, provider string) (store.Account, error) {
	set := s.deps.Settings.Get(r.Context())
	dp := ob.OSProfile
	if dp.OS == "" {
		dp = set.DefaultDeviceProfile
	}
	dp.InstallationID = uuid.NewString()
	acc, err := s.deps.Store.CreateAccount(r.Context(), store.Account{
		CPAAuthName: cpaName, Email: email, IPGroupID: ob.IPGroupID, DeviceProfile: dp, Provider: provider,
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
	ctx := r.Context()
	maxMembers := s.deps.Settings.Get(ctx).MaxAccountMembers
	if err := s.deps.Store.BindAccount(ctx, accID, body.EmployeeID, maxMembers); err != nil {
		switch err {
		case store.ErrAccountFull:
			writeErr(w, http.StatusConflict, fmt.Sprintf("这个账号已经有 %d 人在用，一个账号最多 %d 人共用", maxMembers, maxMembers))
		case store.ErrNotFound:
			writeErr(w, http.StatusNotFound, "account not found")
		default:
			writeErr(w, http.StatusInternalServerError, "bind failed")
		}
		return
	}
	s.audit(r, "account_bind", "account", fmt.Sprint(accID), map[string]any{"employee_id": body.EmployeeID})
	if emp, err := s.deps.Store.GetEmployee(ctx, body.EmployeeID); err == nil && s.deps.Risk != nil {
		portal := strings.TrimRight(s.deps.PublicBaseURL, "/")
		msg := "【AI 网关】你的 AI 账号已经分配好了，现在就能用。还没配置的话，打开 " + portal + "/key 生成 Key，弹窗里有一条命令配好的方式。"
		if accs, err := s.deps.Store.AccountsOf(ctx, emp.ID); err == nil && len(accs) > 1 {
			msg = fmt.Sprintf("【AI 网关】管理员给你加了一个 AI 账号，你现在有 %d 个。不用重新配置：一个账号额度用完时，网关会自动换到另一个。", len(accs))
		}
		go s.deps.Risk.Notify(context.WithoutCancel(ctx), emp.FeishuOpenID, msg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUnbind(w http.ResponseWriter, r *http.Request) {
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		EmployeeID int64 `json:"employee_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EmployeeID == 0 {
		writeErr(w, http.StatusBadRequest, "employee_id required")
		return
	}
	if err := s.deps.Store.UnbindAccount(r.Context(), accID, body.EmployeeID); err != nil {
		writeErr(w, http.StatusInternalServerError, "unbind failed")
		return
	}
	s.audit(r, "account_unbind", "account", fmt.Sprint(accID), map[string]any{"employee_id": body.EmployeeID})
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
