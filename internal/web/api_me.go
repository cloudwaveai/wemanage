package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/store"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	set := s.deps.Settings.Get(r.Context())
	sc, _ := s.computeScope(r.Context(), emp)

	role := "employee"
	if emp.IsSuperAdmin {
		role = "super_admin"
	} else if sc.IsLeader {
		role = "leader"
	}

	resp := map[string]any{
		"id":             emp.ID,
		"name":           emp.Name,
		"email":          emp.Email,
		"avatar_url":     emp.AvatarURL,
		"role":           role,
		"notice_acked":   emp.NoticeAckVersion >= set.NoticeVersion,
		"notice_version": set.NoticeVersion,
		"notice_text":    set.NoticeText,
		"csrf":           currentCSRF(r),
		"frozen":         emp.FrozenAt != nil,
		"frozen_reason":  emp.FrozenReason,
		"frozen_at":      emp.FrozenAt,
		"risk_mode":      set.RiskMode,
		"office_only":    set.OfficeOnly,
		"remote_allowed": emp.RemoteAllowed,
	}
	// Devices awaiting the employee's confirmation.
	pending := 0
	if devs, err := s.deps.Store.ListDevices(r.Context(), emp.ID); err == nil {
		for _, d := range devs {
			if d.Status == store.DevicePending || d.Status == store.DeviceExpired {
				pending++
			}
		}
	}
	resp["pending_devices"] = pending
	// Active key prefix + bound account.
	if k, err := s.deps.Store.ActiveKey(r.Context(), emp.ID); err == nil {
		resp["key_prefix"] = k.KeyPrefix
		resp["key_created_at"] = k.CreatedAt
	}
	if acc, err := s.boundAccount(r.Context(), emp.ID); err == nil {
		resp["account"] = map[string]any{
			"email": acc.Email, "plan_type": acc.PlanType, "status": accountStatus(acc),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAckNotice(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	set := s.deps.Settings.Get(r.Context())
	if err := s.deps.Store.AckNotice(r.Context(), emp.ID, set.NoticeVersion); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot record acknowledgement")
		return
	}
	s.audit(r, "ack_notice", "employee", fmt.Sprint(emp.ID), map[string]any{"version": set.NoticeVersion})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleIssueKey(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	if emp.NoticeAckVersion < s.deps.Settings.Get(r.Context()).NoticeVersion {
		writeErr(w, http.StatusForbidden, "请先确认使用告知")
		return
	}
	if emp.FrozenAt != nil {
		writeErr(w, http.StatusForbidden, "你的 Key 已被冻结，需要管理员核实后解冻")
		return
	}
	plain, key, err := s.deps.Store.IssueKey(r.Context(), emp.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot issue key")
		return
	}
	s.audit(r, "issue_key", "employee", fmt.Sprint(emp.ID), nil)
	// Plaintext is returned exactly once.
	writeJSON(w, http.StatusOK, map[string]any{"key": plain, "prefix": key.KeyPrefix})
}

func (s *Server) handleMyUsage(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	from, to := usageRange(r)
	u, err := s.deps.Store.Usage(r.Context(), from, to, []int64{emp.ID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleMyQuota(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	acc, err := s.boundAccount(r.Context(), emp.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no account bound")
		return
	}
	resp := map[string]any{"status": accountStatus(acc)}
	if acc.RateLimitedUntil != nil {
		resp["rate_limited_until"] = acc.RateLimitedUntil
	}
	if q, err := s.deps.Store.LatestQuota(r.Context(), acc.ID); err == nil {
		resp["quota"] = q
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMyReports(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	reports, err := s.deps.Store.GetDailyReports(r.Context(), emp.ID, 30)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reports query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

func (s *Server) handleConfigSnippets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := s.deps.PublicBaseURL + "/v1"
	provider := ""
	var models []string
	acc, accErr := s.boundAccount(ctx, currentEmployee(r).ID)
	if accErr == nil {
		provider = acc.Provider
		if s.deps.Models != nil {
			if ids, err := s.deps.Models(ctx, acc); err == nil {
				models = ids
			}
		}
	}
	chatgpt := accErr != nil || acc.IsChatGPT()
	model := pickDefaultModel(models, chatgpt)

	modelLine := fmt.Sprintf("model = %q", model)
	// ChatGPT accounts use the WebSocket transport like the official client;
	// other providers (and the kill switch) stay on HTTP.
	wsLine := "# 与官方 Codex 一致，走 WebSocket（网关不支持时 Codex 会自动退回 HTTP）\nsupports_websockets = true"
	if s.deps.Settings.Get(ctx).DisableWebsocket {
		wsLine = "# 公司暂时关闭了 WebSocket，走普通 HTTP\nsupports_websockets = false"
	}
	if !chatgpt {
		wsLine = "# 非 ChatGPT 账号走普通 HTTP\nsupports_websockets = false"
		if model != "" {
			modelLine = fmt.Sprintf("# 你的账号是 %s，可用模型：%s\n%s", acc.Provider, strings.Join(models, "、"), modelLine)
		} else {
			modelLine = fmt.Sprintf(`# 你的账号不是 ChatGPT（%s）。查看可用模型：
#   curl -s %s/models -H "Authorization: Bearer $AISEAT_API_KEY"
# 然后把下面改成其中一个 id
model = "填入模型 id"`, acc.Provider, base)
		}
	}
	codex := fmt.Sprintf(`model_provider = "company"
%s

[model_providers.company]
name = "Company Gateway"
base_url = "%s"
wire_api = "responses"
# Codex 从这个环境变量读取你的 sk-emp- Key，并作为 Bearer 发给网关
env_key = "AISEAT_API_KEY"
env_key_instructions = "在门户「我的 Key」生成后：export AISEAT_API_KEY=sk-emp-..."
requires_openai_auth = false
%s`, modelLine, base, wsLine)

	note := "在 Cursor / Cherry Studio 等工具中把 Base URL 设为上面地址，API Key 填你的 sk-emp- key。第三方客户端风险较高，建议优先使用 Codex。"
	allowed := s.deps.Settings.Get(ctx).AllowNonCodex
	if !allowed {
		note = "公司目前只允许官方 Codex 客户端，Cursor / Cherry Studio 等第三方工具的请求会被拦截（client_not_allowed）。确有需要请联系管理员。"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":      base,
		"provider":      provider,
		"models":        nonNil(models),
		"default_model": model,
		"codex_toml":    codex,
		"codex_env":     "export AISEAT_API_KEY=\"你的 sk-emp-... key\"",
		// CC Switch builds its own config.toml from these (the key is added in
		// the browser and never sent back to the server).
		"ccswitch": map[string]any{
			"name":     "AI 订阅网关",
			"endpoint": base,
			"homepage": s.deps.PublicBaseURL,
		},
		"third_party": map[string]any{
			"base_url": base,
			"allowed":  allowed,
			"note":     note,
		},
	})
}

// pickDefaultModel chooses the model a new setup should use: the newest
// coding model (…codex for ChatGPT, …-code for others), skipping speed/size
// variants; otherwise the first listed. With no list it falls back to a
// ChatGPT default, or "" for other providers (their ids are unknown).
func pickDefaultModel(ids []string, chatgpt bool) string {
	want := "-code"
	if chatgpt {
		want = "codex"
	}
	best := ""
	for _, id := range ids {
		l := strings.ToLower(id)
		if !strings.Contains(l, want) {
			continue
		}
		skip := false
		for _, v := range []string{"mini", "max", "highspeed", "spark", "lite", "nano"} {
			if strings.Contains(l, v) {
				skip = true
				break
			}
		}
		if !skip && (best == "" || newerModel(id, best)) {
			best = id
		}
	}
	switch {
	case best != "":
		return best
	case len(ids) > 0:
		return ids[0]
	case chatgpt:
		return "gpt-5.1-codex"
	default:
		return ""
	}
}

// newerModel compares the version numbers embedded in two model ids
// ("kimi-k2.8-code" > "kimi-k2.7-code", "gpt-5.1-codex" > "gpt-5-codex").
func newerModel(a, b string) bool {
	va, vb := modelVersion(a), modelVersion(b)
	for i := 0; i < len(va) && i < len(vb); i++ {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	if len(va) != len(vb) {
		return len(va) > len(vb)
	}
	return len(a) < len(b) // same version: the plainer name
}

var digitsRe = regexp.MustCompile(`\d+`)

func modelVersion(id string) []int {
	var v []int
	for _, d := range digitsRe.FindAllString(id, -1) {
		n, _ := strconv.Atoi(d)
		v = append(v, n)
	}
	return v
}

// --- helpers ---

func (s *Server) boundAccount(ctx context.Context, employeeID int64) (store.Account, error) {
	var accID int64
	err := s.deps.Store.Pool.QueryRow(ctx,
		`SELECT id FROM upstream_accounts WHERE employee_id=$1`, employeeID).Scan(&accID)
	if err != nil {
		return store.Account{}, errors.New("no account")
	}
	return s.deps.Store.GetAccount(ctx, accID)
}

func accountStatus(a store.Account) string {
	switch {
	case a.Status == "disabled":
		return "disabled"
	case a.NeedsLogin:
		return "needs_login"
	case a.RateLimitedUntil != nil && a.RateLimitedUntil.After(time.Now()):
		return "rate_limited"
	default:
		return "active"
	}
}

func usageRange(r *http.Request) (time.Time, time.Time) {
	to := time.Now()
	days := 7
	switch r.URL.Query().Get("range") {
	case "1", "today":
		days = 1
	case "30":
		days = 30
	}
	return to.Add(-time.Duration(days) * 24 * time.Hour), to
}
