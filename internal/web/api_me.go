package web

import (
	"context"
	"encoding/json"
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
	accs, _ := s.deps.Store.AccountsOf(r.Context(), emp.ID)
	list := make([]map[string]any, 0, len(accs))
	for _, acc := range accs {
		list = append(list, map[string]any{
			"id": acc.ID, "email": acc.Email, "plan_type": acc.PlanType, "provider": acc.Provider, "status": accountStatus(acc),
		})
	}
	resp["accounts"] = list
	// The getting-started guide checks Codex off once a request got through,
	// and says which client and version it came from.
	if l, err := s.deps.Store.LastSuccess(r.Context(), emp.ID); err == nil {
		client, version, cli := codexClient(l.UserAgent)
		resp["last_success"] = map[string]any{"at": l.At, "client": client, "version": version, "cli": cli}
	}
	resp["codex_version"] = set.CodexProfile.Version
	writeJSON(w, http.StatusOK, resp)
}

// codexClient names the client and version in a Codex User-Agent such as
// "codex_cli_rs/0.159.0 (Mac OS 15.6.0; arm64) iTerm.app/3.5" or
// "Codex Desktop/1.3.0 …"; cli reports whether the version is the Codex CLI's
// (the desktop app numbers its own releases).
func codexClient(ua string) (client, version string, cli bool) {
	product, rest, ok := strings.Cut(strings.TrimSpace(ua), "/")
	if !ok {
		return "", "", false
	}
	version, _, _ = strings.Cut(rest, " ")
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "codex_cli_rs", "codex-tui", "codex_exec":
		return "Codex 命令行", version, true
	case "codex_vscode":
		return "Codex 编辑器插件", version, true
	case "codex desktop":
		return "Codex 桌面版", version, false
	}
	return strings.TrimSpace(product), version, false
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

// handleMyQuota lists the employee's accounts with their latest quota.
func (s *Server) handleMyQuota(w http.ResponseWriter, r *http.Request) {
	emp := currentEmployee(r)
	accs, err := s.deps.Store.AccountsOf(r.Context(), emp.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(accs))
	for _, acc := range accs {
		a := map[string]any{"id": acc.ID, "email": acc.Email, "plan_type": acc.PlanType, "provider": acc.Provider,
			"status": accountStatus(acc), "rate_limited_until": acc.RateLimitedUntil}
		if q, err := s.deps.Store.LatestQuota(r.Context(), acc.ID); err == nil {
			a["quota"] = q
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
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
	accs                          []store.Account
	thirdParty                    bool // some account is open to clients other than Codex
	wsLine, toolsLine             string
}

func (s *Server) codexSetup(ctx context.Context, employeeID int64) codexSetup {
	cs := codexSetup{base: s.deps.PublicBaseURL + "/v1", source: "account"}
	accs, _ := s.deps.Store.AccountsOf(ctx, employeeID)
	cs.accs = accs
	set := s.deps.Settings.Get(ctx)
	cs.thirdParty = set.AllowNonCodex
	for _, a := range accs {
		cs.thirdParty = cs.thirdParty || a.AllowThirdParty
	}
	if len(accs) > 0 {
		// Settings follow the first ChatGPT account (else the first account);
		// models are every account's, as their policies allow.
		cs.provider = accs[0].Provider
		cs.chatgpt = false
		for _, a := range accs {
			if a.IsChatGPT() {
				cs.provider, cs.chatgpt = a.Provider, true
				break
			}
		}
		cs.models = s.employeeModels(ctx, accs)
	} else {
		cs.chatgpt = true
		if s.deps.ModelCatalog != nil {
			// No account yet: show what a ChatGPT account typically offers.
			cs.source = "catalog"
			if ms, err := s.deps.ModelCatalog(ctx, "codex"); err == nil {
				cs.models = ms
			}
		}
	}
	// Newest first.
	sort.SliceStable(cs.models, func(i, j int) bool { return cs.models[i].Created > cs.models[j].Created })
	preferred := set.DefaultCodexModel
	if !cs.chatgpt {
		preferred = providerDefaultModel[cs.provider]
	}
	cs.model = pickDefaultModel(cs.models, cs.chatgpt, preferred)

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
	// luna-class models search with Codex's standalone web.run tool, which
	// calls {base_url}/alpha/search; the gateway serves that for ChatGPT accounts.
	if cs.chatgpt {
		cs.toolsLine = "\n# 开启 Codex 的生图和联网搜索（这个请求头只给 Codex 看，网关会去掉，不会发给上游）\n" +
			`http_headers = { "x-openai-actor-authorization" = "aiseat" }` + "\n" +
			"supports_standalone_web_search = true"
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
	chatgpt, wsLine, toolsLine := cs.chatgpt, cs.wsLine, cs.toolsLine
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
			modelLine = fmt.Sprintf("# 你的账号是 %s，可用模型：%s\n%s", provider, strings.Join(ids, "、"), modelLine)
		} else {
			modelLine = fmt.Sprintf(`# 你的账号不是 ChatGPT（%s）。查看可用模型：
#   curl -s %s/models -H "Authorization: Bearer 你的 Key"
# 然后把下面改成其中一个 id
model = "填入模型 id"`, provider, base)
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

	allowed := cs.thirdParty
	note := "网关同时提供 OpenAI 接口（Responses、Chat Completions）和 Anthropic 接口（Messages）：Claude Code、OpenCode、Command Code、Cherry Studio、Cline 等工具填上面的 Base URL 和你的 Key 就能用。下面有现成的配置。"
	if !allowed {
		note = "你的 AI 账号目前只开放给官方 Codex 客户端，Claude Code、OpenCode、Command Code 等工具的请求会被拦截。确有需要请联系管理员开通。"
	}
	thirdModels := map[string]map[string]any{}
	ccModels := map[string]map[string]any{}
	for _, id := range ids {
		thirdModels[id] = map[string]any{"name": id}
		ccModels[id] = map[string]any{}
	}
	if len(ids) == 0 && model != "" {
		thirdModels[model] = map[string]any{"name": model}
		ccModels[model] = map[string]any{}
	}
	// OpenCode: ~/.config/opencode/opencode.json, the Responses API via
	// @ai-sdk/openai; the browser fills in {{KEY}} and {{MODEL}}.
	opencode, _ := json.MarshalIndent(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{"aiseat": map[string]any{
			"npm": "@ai-sdk/openai", "name": "公司 AI 网关",
			"options": map[string]any{"baseURL": base, "apiKey": "{{KEY}}"},
			"models":  thirdModels,
		}},
		"model": "aiseat/{{MODEL}}",
	}, "", "  ")
	// Command Code: ~/.commandcode/providers.json, wire "openai-responses";
	// the key comes from an environment variable.
	commandcode, _ := json.MarshalIndent(map[string]any{
		"provider": map[string]any{"aiseat": map[string]any{
			"name": "公司 AI 网关", "baseURL": base, "api": "openai-responses",
			"apiKey": "$AISEAT_API_KEY", "models": ccModels,
		}},
	}, "", "  ")
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
			"base_url":         base,
			"allowed":          allowed,
			"note":             note,
			"opencode_json":    string(opencode),
			"commandcode_json": string(commandcode),
			"commandcode_env":  `export AISEAT_API_KEY="{{KEY}}"`,
			// Claude Code through the gateway's Anthropic endpoint, under its
			// own command so the plain "claude" (and its login) stays as it is.
			"claude_code_sh": claudeCodeSh(s.deps.PublicBaseURL),
			"claude_code_ps": claudeCodePs(s.deps.PublicBaseURL),
		},
	})
}

// claudeCodeSh is a shell alias starting Claude Code on the gateway: every
// Claude model tier maps to the chosen model, and nothing is written into
// Claude Code's own settings. The browser fills in {{KEY}} and {{MODEL}}.
func claudeCodeSh(portal string) string {
	return `# 公司 AI 网关上的 Claude Code：用 claude-gw 启动，原来的 claude 不受影响
alias claude-gw='ANTHROPIC_BASE_URL="` + portal + `" ANTHROPIC_AUTH_TOKEN="{{KEY}}" ANTHROPIC_MODEL="{{MODEL}}" ANTHROPIC_DEFAULT_OPUS_MODEL="{{MODEL}}" ANTHROPIC_DEFAULT_SONNET_MODEL="{{MODEL}}" ANTHROPIC_DEFAULT_HAIKU_MODEL="{{MODEL}}" CLAUDE_CODE_SUBAGENT_MODEL="{{MODEL}}" CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 claude'`
}

// claudeCodePs is claudeCodeSh for a PowerShell profile.
func claudeCodePs(portal string) string {
	return `# 公司 AI 网关上的 Claude Code：用 claude-gw 启动，原来的 claude 不受影响
function claude-gw {
  $vars = @{
    ANTHROPIC_BASE_URL = '` + portal + `'; ANTHROPIC_AUTH_TOKEN = '{{KEY}}'; ANTHROPIC_MODEL = '{{MODEL}}'
    ANTHROPIC_DEFAULT_OPUS_MODEL = '{{MODEL}}'; ANTHROPIC_DEFAULT_SONNET_MODEL = '{{MODEL}}'
    ANTHROPIC_DEFAULT_HAIKU_MODEL = '{{MODEL}}'; CLAUDE_CODE_SUBAGENT_MODEL = '{{MODEL}}'
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = '1'
  }
  $old = @{}
  foreach ($k in $vars.Keys) { $old[$k] = [Environment]::GetEnvironmentVariable($k, 'Process'); Set-Item "env:$k" $vars[$k] }
  try { claude @args } finally { foreach ($k in $vars.Keys) { if ($old[$k]) { Set-Item "env:$k" $old[$k] } else { Remove-Item "env:$k" -ErrorAction SilentlyContinue } } }
}`
}

// providerDefaultModel is the everyday model of providers whose lists do not
// make it obvious.
var providerDefaultModel = map[string]string{
	"antigravity": "gemini-3.1-pro-low",
	"xai":         "grok-4.7",
	"meta":        "muse-spark-1.3",
	"devin":       "devin/swe-2",
}

// pickDefaultModel chooses the model one-click setup recommends.
//
// ChatGPT: the configured model when the account has it, else the newest
// model of the same family (the last "-" part: "gpt-6-luna" -> "luna"),
// else the newest model that is not a review helper; with no list, the
// configured model itself.
//
// Others: the provider's everyday model (providerDefaultModel) when listed,
// else the newest "-code" model, skipping speed/size variants (Kimi), else
// the first listed; with no list, "" (their ids are unknown).
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
	for _, m := range models {
		if preferred != "" && m.ID == preferred {
			return preferred
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
