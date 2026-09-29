package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
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
		"csrf":           currentCSRF(r),
		"frozen":         emp.FrozenAt != nil,
		"frozen_reason":  emp.FrozenReason,
		"frozen_at":      emp.FrozenAt,
		"risk_mode":      set.RiskMode,
		"office_only":    set.OfficeOnly,
		"remote_allowed": emp.RemoteAllowed,
		"weekly_report":  !emp.WeeklyReportOff,
		"quota_alert":    emp.QuotaAlert,
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

func (s *Server) handleIssueKey(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
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
	series, err := s.usageSeries(r.Context(), from, to, []int64{emp.ID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		store.UsageSummary
		Series usageSeriesResp `json:"series"`
	}{u, series})
}

// usageSeriesResp is a request/token time series and its bucket size.
type usageSeriesResp struct {
	BucketSeconds int64               `json:"bucket_seconds"`
	Points        []store.SeriesPoint `json:"points"`
}

// usageSeries draws [from,to) at a resolution that suits the data: the
// window starts one bucket before the first request (not at the start of the
// range), and the bucket grows with it — 5 or 15 minutes for a few hours,
// hours for a few days, days beyond that — so a single busy day reads as a
// curve rather than a lone point. Buckets follow the company's local clock.
func (s *Server) usageSeries(ctx context.Context, from, to time.Time, ids []int64) (usageSeriesResp, error) {
	first, ok, err := s.deps.Store.FirstRequestAt(ctx, from, to, ids)
	if err != nil || !ok {
		return usageSeriesResp{Points: []store.SeriesPoint{}}, err
	}
	bucket := seriesBucket(to.Sub(first))
	loc := s.deps.Loc
	if loc == nil {
		loc = time.Local
	}
	origin := time.Date(2000, 1, 1, 0, 0, 0, 0, loc)
	points, err := s.deps.Store.UsageSeries(ctx, first.Add(-bucket), to, ids, bucket, origin)
	if err != nil {
		return usageSeriesResp{}, err
	}
	return usageSeriesResp{BucketSeconds: int64(bucket / time.Second), Points: points}, nil
}

// seriesBucket picks the bucket for a window of the given length.
func seriesBucket(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return 5 * time.Minute
	case span <= 8*time.Hour:
		return 15 * time.Minute
	case span <= 4*24*time.Hour:
		return time.Hour
	default:
		return 24 * time.Hour
	}
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

// handleWeeklyReportPref lets an employee turn their weekly recap off or on.
func (s *Server) handleWeeklyReportPref(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	emp := currentEmployee(r)
	if err := s.deps.Store.SetWeeklyReportOff(r.Context(), emp.ID, !body.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"weekly_report": body.Enabled})
}

// handleQuotaAlertPref turns the employee's 80% quota reminder on or off.
func (s *Server) handleQuotaAlertPref(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	emp := currentEmployee(r)
	if err := s.deps.Store.SetQuotaAlert(r.Context(), emp.ID, body.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota_alert": body.Enabled})
}

// handleMyStatus answers "can I use it right now, and if not, why": key,
// account, quota, devices waiting for confirmation, and the latest success
// and failure. It is the employee's own view; nothing here goes to anyone else.
func (s *Server) handleMyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	emp := currentEmployee(r)
	resp := map[string]any{"frozen": emp.FrozenAt != nil, "frozen_reason": emp.FrozenReason, "quota_alert": emp.QuotaAlert}

	_, err := s.deps.Store.ActiveKey(ctx, emp.ID)
	resp["has_key"] = err == nil

	account := map[string]any{"bound": false}
	if acc, err := s.boundAccount(ctx, emp.ID); err == nil {
		account = map[string]any{"bound": true, "provider": acc.Provider, "status": accountStatus(acc),
			"rate_limited_until": acc.RateLimitedUntil}
		if q, err := s.deps.Store.LatestQuota(ctx, acc.ID); err == nil {
			resp["quota"] = q
		}
	}
	resp["account"] = account

	pending := []map[string]any{}
	if devs, err := s.deps.Store.ListDevices(ctx, emp.ID); err == nil {
		for _, d := range devs {
			if d.Status == store.DevicePending || d.Status == store.DeviceExpired {
				pending = append(pending, map[string]any{"id": d.ID, "label": d.Label, "last_ip": d.LastIP,
					"first_seen": d.FirstSeen, "status": d.Status})
			}
		}
	}
	resp["pending_devices"] = pending

	brief := func(b *store.RequestBrief) any {
		if b == nil {
			return nil
		}
		return map[string]any{"at": b.At, "client_kind": b.ClientKind, "model": b.Model,
			"status_code": b.StatusCode, "error_type": b.ErrorType}
	}
	ok, failed, _ := s.deps.Store.LastRequests(ctx, emp.ID)
	resp["last_ok"] = brief(ok)
	// A failure only matters until something works again.
	if failed != nil && (ok == nil || failed.At.After(ok.At)) {
		resp["last_failure"] = brief(failed)
	} else {
		resp["last_failure"] = nil
	}
	loc := s.deps.Loc
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	today, _ := s.deps.Store.RequestsSince(ctx, emp.ID, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc))
	resp["today_requests"] = today
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMyReports(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	reports, err := s.deps.Store.GetReports(r.Context(), emp.ID, 30)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reports query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

// codexSetup is what every way of configuring Codex shares: the gateway's
// base URL, the account's models and the recommended one, and the provider
// settings that depend on the account.
type codexSetup struct {
	base, provider, source, model string
	models                        []store.ModelInfo
	chatgpt                       bool
	acc                           store.Account
	wsLine, toolsLine             string
}

func (s *Server) codexSetup(ctx context.Context, employeeID int64) codexSetup {
	cs := codexSetup{base: s.deps.PublicBaseURL + "/v1", source: "account"}
	acc, accErr := s.boundAccount(ctx, employeeID)
	if accErr == nil {
		cs.acc, cs.provider = acc, acc.Provider
		if s.deps.Models != nil {
			if ms, err := s.deps.Models(ctx, acc); err == nil {
				cs.models = ms
			}
		}
	} else if s.deps.ModelCatalog != nil {
		// No account yet: show what a ChatGPT account typically offers.
		cs.source = "catalog"
		if ms, err := s.deps.ModelCatalog(ctx, "codex"); err == nil {
			cs.models = ms
		}
	}
	// Newest first.
	sort.SliceStable(cs.models, func(i, j int) bool { return cs.models[i].Created > cs.models[j].Created })
	cs.chatgpt = accErr != nil || acc.IsChatGPT()
	set := s.deps.Settings.Get(ctx)
	cs.model = pickDefaultModel(cs.models, cs.chatgpt, set.DefaultCodexModel)

	// ChatGPT accounts use the WebSocket transport like the official client;
	// other providers (and the kill switch) stay on HTTP.
	cs.wsLine = "# 与官方 Codex 一致，走 WebSocket（网关不支持时 Codex 会自动退回 HTTP）\nsupports_websockets = true"
	if set.DisableWebsocket {
		cs.wsLine = "# 公司暂时关闭了 WebSocket，走普通 HTTP\nsupports_websockets = false"
	}
	if !cs.chatgpt {
		cs.wsLine = "# 非 ChatGPT 账号走普通 HTTP\nsupports_websockets = false"
	}
	// Codex only offers its image generation and web search tools for OpenAI
	// providers; a custom provider qualifies when it sends this header and
	// does not use OpenAI login. The gateway strips it before going upstream.
	if cs.chatgpt {
		cs.toolsLine = "\n# 开启 Codex 的生图和联网搜索（这个请求头只给 Codex 看，网关会去掉，不会发给上游）\n" +
			`http_headers = { "x-openai-actor-authorization" = "aiseat" }`
	}
	return cs
}

// hasModel reports whether id is one of the setup's models (or, with no
// list, the recommended one).
func (cs codexSetup) hasModel(id string) bool {
	if len(cs.models) == 0 {
		return id == cs.model
	}
	for _, m := range cs.models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) handleConfigSnippets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cs := s.codexSetup(ctx, currentEmployee(r).ID)
	base, provider, source, model, models := cs.base, cs.provider, cs.source, cs.model, cs.models
	chatgpt, acc, wsLine, toolsLine := cs.chatgpt, cs.acc, cs.wsLine, cs.toolsLine
	ids := make([]string, len(models))
	modelList := make([]map[string]any, len(models))
	for i, m := range models {
		ids[i] = m.ID
		modelList[i] = map[string]any{"id": m.ID, "name": m.Name, "created": m.Created}
	}

	// The manual config is the install script's, as a template: the browser
	// fills in {{KEY}} and {{MODEL}} from what the employee entered.
	modelLine := `model = "{{MODEL}}"`
	if !chatgpt {
		if model != "" {
			modelLine = fmt.Sprintf("# 你的账号是 %s，可用模型：%s\n%s", acc.Provider, strings.Join(ids, "、"), modelLine)
		} else {
			modelLine = fmt.Sprintf(`# 你的账号不是 ChatGPT（%s）。查看可用模型：
#   curl -s %s/models -H "Authorization: Bearer 你的 Key"
# 然后把下面改成其中一个 id
model = "填入模型 id"`, acc.Provider, base)
		}
	}
	codex := "model_provider = \"aiseat\"\n" + modelLine + "\n\n" + cs.providerTable("{{KEY}}")

	// CC Switch's import writes a fixed config (key in auth.json, OpenAI login
	// required), which rules the tools out. For ChatGPT accounts we offer a
	// full replacement to paste into CC Switch's editor; the browser fills in
	// {{KEY}} and {{MODEL}}, and the key never comes back here.
	ccToml := ""
	if chatgpt {
		ccToml = fmt.Sprintf(`model_provider = "custom"
model = "{{MODEL}}"
disable_response_storage = true

[model_providers.custom]
name = "AI 订阅网关"
base_url = "%s"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "{{KEY}}"
%s%s`, base, wsLine, toolsLine)
	}

	note := "在 Cursor / Cherry Studio 等工具中把 Base URL 设为上面地址，API Key 填你的 sk-emp- key。第三方客户端风险较高，建议优先使用 Codex。"
	allowed := s.deps.Settings.Get(ctx).AllowNonCodex
	if !allowed {
		note = "公司目前只允许官方 Codex 客户端，Cursor / Cherry Studio 等第三方工具的请求会被拦截（client_not_allowed）。确有需要请联系管理员。"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":      base,
		"provider":      provider,
		"models":        modelList,
		"models_source": source,
		"default_model": model,
		// Manual config.toml; the browser fills in {{KEY}} and {{MODEL}}.
		"codex_toml": codex,
		// CC Switch builds its own config.toml from these (the key is added in
		// the browser and never sent back to the server).
		"ccswitch": map[string]any{
			"name":     "AI 订阅网关",
			"endpoint": base,
			"homepage": s.deps.PublicBaseURL,
			// Replacement config.toml enabling image generation and web search
			// ("" when the account cannot use them); the browser fills {{KEY}} and {{MODEL}}.
			"config_toml": ccToml,
		},
		"third_party": map[string]any{
			"base_url": base,
			"allowed":  allowed,
			"note":     note,
		},
	})
}

// pickDefaultModel chooses the model one-click setup recommends.
//
// ChatGPT: the configured model when the account has it, else the newest
// model of the same family (the last "-" part: "gpt-6-luna" -> "luna"),
// else the newest model that is not a review helper; with no list, the
// configured model itself.
//
// Others (e.g. Kimi): the newest "-code" model, skipping speed/size variants,
// else the first listed; with no list, "" (their ids are unknown).
func pickDefaultModel(models []store.ModelInfo, chatgpt bool, preferred string) string {
	if chatgpt {
		if len(models) == 0 {
			return preferred
		}
		family := ""
		if i := strings.LastIndex(preferred, "-"); i >= 0 {
			family = "-" + preferred[i+1:]
		}
		var sameFamily, any *store.ModelInfo
		for i := range models {
			m := &models[i]
			if m.ID == preferred {
				return preferred
			}
			if family != "" && strings.HasSuffix(m.ID, family) && newerInfo(m, sameFamily) {
				sameFamily = m
			}
			if !strings.Contains(m.ID, "review") && newerInfo(m, any) {
				any = m
			}
		}
		switch {
		case sameFamily != nil:
			return sameFamily.ID
		case any != nil:
			return any.ID
		default:
			return models[0].ID
		}
	}
	best := ""
	for _, m := range models {
		l := strings.ToLower(m.ID)
		if !strings.Contains(l, "-code") {
			continue
		}
		skip := false
		for _, v := range []string{"mini", "max", "highspeed", "spark", "lite", "nano"} {
			if strings.Contains(l, v) {
				skip = true
				break
			}
		}
		if !skip && (best == "" || newerModel(m.ID, best)) {
			best = m.ID
		}
	}
	switch {
	case best != "":
		return best
	case len(models) > 0:
		return models[0].ID
	default:
		return ""
	}
}

// newerInfo reports whether m is newer than cur (nil counts as older): by
// release time, then by the version in the id.
func newerInfo(m, cur *store.ModelInfo) bool {
	if cur == nil {
		return true
	}
	if m.Created != cur.Created {
		return m.Created > cur.Created
	}
	return newerModel(m.ID, cur.ID)
}

// newerModel compares the version numbers embedded in two model ids
// ("kimi-k2.8-code" > "kimi-k2.7-code", "gpt-6-luna" > "gpt-5.6-luna").
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
