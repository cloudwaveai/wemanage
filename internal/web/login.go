package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/store"
)

// Adding an account: every login CPA offers goes through one flow. The admin
// starts it, logs in to the provider in their own browser, and completes it.
// Redirect logins end on a localhost page on the admin's machine (CPA runs
// elsewhere), so the admin pastes that page's URL back; device logins are
// finished by CPA itself, so completing just waits for it.

// loginProvider is one kind of account CPA can log in.
type loginProvider struct {
	ID    string `json:"id"` // CPA auth-file type and oauth-callback provider
	Label string `json:"label"`
	Note  string `json:"note"`
	// Paste: a redirect login, its final URL (starting with Callback) is
	// pasted back within CPA's 5 minutes. Otherwise a device login.
	Paste    bool   `json:"paste"`
	Callback string `json:"callback,omitempty"`
	path     string // CPA management path that starts the login
}

// loginProviders are CPA's logins except Claude, which the company does not
// proxy.
var loginProviders = []loginProvider{
	{ID: "codex", Label: "ChatGPT", path: "/codex-auth-url", Paste: true, Callback: "http://localhost:1455/auth/callback",
		Note: "ChatGPT Plus / Pro / Team 账号，员工用 Codex。有 5 小时 / 每周额度显示。"},
	{ID: "kimi", Label: "Kimi Code", path: "/kimi-auth-url",
		Note: "kimi.com 的 Kimi Code 会员（K2 / K3 系列模型）。"},
	{ID: "kimi-ai", Label: "Kimi Code 国际版", path: "/kimi-ai-auth-url",
		Note: "kimi.ai 国际版账号，模型和国内版相同。"},
	{ID: "antigravity", Label: "Antigravity（Google）", path: "/antigravity-auth-url", Paste: true, Callback: "http://localhost:51121/oauth-callback",
		Note: "Google 账号登录 Antigravity，提供 Gemini 3 系列等模型。"},
	{ID: "xai", Label: "Grok（xAI）", path: "/xai-auth-url",
		Note: "x.ai 账号（SuperGrok 等订阅），提供 Grok 4 系列和 Grok Imagine 生图。"},
	{ID: "meta", Label: "Meta Muse", path: "/meta-auth-url",
		Note: "Meta 账号的 Muse Code 订阅（Muse Spark 系列模型）。"},
	{ID: "devin", Label: "Devin", path: "/devin-auth-url", Paste: true, Callback: "http://127.0.0.1:8317/callback",
		Note: "Devin（Cognition / Windsurf）账号，模型名带 devin/ 前缀。有每日 / 每周额度。"},
}

func findLoginProvider(id string) (loginProvider, bool) {
	for _, p := range loginProviders {
		if p.ID == id {
			return p, true
		}
	}
	return loginProvider{}, false
}

// providerLabel names a provider for people ("ChatGPT", "Grok（xAI）").
func providerLabel(id string) string {
	if id == "" {
		id = store.ProviderCodex
	}
	if p, ok := findLoginProvider(id); ok {
		return p.Label
	}
	return id
}

func (s *Server) handleLoginProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": loginProviders})
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider  string              `json:"provider"`
		IPGroupID int64               `json:"ip_group_id"`
		OS        store.DeviceProfile `json:"os_profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPGroupID == 0 {
		writeErr(w, http.StatusBadRequest, "ip_group_id required")
		return
	}
	s.startLogin(w, r, body.Provider, body.IPGroupID, body.OS)
}

func (s *Server) startLogin(w http.ResponseWriter, r *http.Request, provider string, ipGroupID int64, os store.DeviceProfile) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	p, ok := findLoginProvider(provider)
	if !ok {
		writeErr(w, http.StatusBadRequest, "不支持的账号类型："+provider)
		return
	}
	ctx := r.Context()
	// Accounts that exist now; the one that appears after login is the new one.
	known, err := s.deps.CPA.ListAuthFiles(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot list CPA auth files: "+err.Error())
		return
	}
	ls, err := s.deps.CPA.StartLogin(ctx, p.path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "CPA 没能开始 "+p.Label+" 登录："+err.Error())
		return
	}
	names := make([]string, 0, len(known))
	for _, f := range known {
		names = append(names, f.Name)
	}
	emp := currentEmployee(r)
	if err := s.deps.Store.CreateOnboarding(ctx, store.Onboarding{
		State: ls.State, IPGroupID: ipGroupID, OSProfile: os, KnownAuthNames: names, CreatedBy: &emp.ID, Provider: p.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot record onboarding")
		return
	}
	s.audit(r, "account_login_start", "ip_group", fmt.Sprint(ipGroupID), map[string]any{"provider": p.ID})
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": p.ID, "auth_url": ls.URL, "state": ls.State, "user_code": ls.UserCode,
		"expires_in": ls.ExpiresIn, "paste": p.Paste, "callback": p.Callback,
	})
}

func (s *Server) handleLoginComplete(w http.ResponseWriter, r *http.Request) {
	if s.deps.CPA == nil {
		writeErr(w, http.StatusServiceUnavailable, "CPA not configured")
		return
	}
	var body struct {
		State       string `json:"state"`
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.State == "" {
		writeErr(w, http.StatusBadRequest, "state required")
		return
	}
	ctx := r.Context()
	ob, err := s.deps.Store.GetOnboarding(ctx, body.State)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unknown onboarding state")
		return
	}
	p, ok := findLoginProvider(ob.Provider)
	if !ok {
		writeErr(w, http.StatusBadRequest, "不支持的账号类型："+ob.Provider)
		return
	}
	if p.Paste {
		redirect := strings.TrimSpace(body.RedirectURL)
		if redirect == "" {
			writeErr(w, http.StatusBadRequest, "请粘贴登录后浏览器地址栏里的完整地址")
			return
		}
		// CPA finds the login by the state inside the pasted URL; one from an
		// earlier link would land on that link's (long expired) login.
		if u, perr := url.Parse(redirect); perr == nil {
			if st := u.Query().Get("state"); st != "" && st != body.State {
				writeErr(w, http.StatusBadRequest, "这个回调地址来自之前的另一个登录链接。请用上面「复制登录链接」里的链接重新登录，再粘回新的回调地址。")
				return
			}
		}
		if err := s.deps.CPA.PostOAuthCallback(ctx, p.ID, redirect); err != nil {
			status, msg := oauthCallbackError(err, p)
			writeErr(w, status, msg)
			return
		}
	}
	// CPA exchanges the code (or finishes the device login) in the background.
	status, msg := "pending", ""
	wait := 30 * time.Second
	if !p.Paste {
		wait = 20 * time.Second
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(time.Second) {
		if status, msg, _ = s.deps.CPA.AuthStatusDetail(ctx, body.State); status != "pending" {
			break
		}
	}
	switch status {
	case "pending":
		if p.Paste {
			writeErr(w, http.StatusGatewayTimeout, "CPA 还在处理这次登录，请稍等几秒再点一次「完成」。")
		} else {
			writeErr(w, http.StatusConflict, "还没完成授权：请在打开的页面里登录并确认授权，再点「我已授权，完成」。")
		}
		return
	case "error":
		writeErr(w, http.StatusGone, p.Label+" 没有接受这次登录（"+msg+"）。请点「重新获取登录链接」再登录一次。")
		return
	}
	files, err := s.deps.CPA.ListAuthFiles(ctx)
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
		if files[i].Type == p.ID && !known[files[i].Name] {
			newFile = &files[i]
			break
		}
	}
	if newFile == nil {
		writeErr(w, http.StatusBadGateway, "CPA 里没有出现新的 "+p.Label+" 账号。如果这个账号之前已经加过，CPA 会更新原来那个，不会新增。")
		return
	}
	acc, err := s.finishAccount(r, ob, newFile.Name, newFile.Email, p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.CompleteOnboarding(ctx, body.State, acc.ID)
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"email": acc.Email, "via": p.ID, "cpa_name": newFile.Name})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "email": acc.Email, "provider": p.ID})
}

// oauthCallbackError explains a callback CPA refused. 410 means this login is
// over and the admin has to start again with a new link.
func oauthCallbackError(err error, p loginProvider) (int, string) {
	var ae *cpa.APIError
	if !errors.As(err, &ae) {
		return http.StatusBadGateway, "回调失败：" + err.Error()
	}
	switch {
	case strings.Contains(ae.Message, "Timeout waiting"), ae.Status == http.StatusNotFound:
		return http.StatusGone, "登录链接已过期：生成链接后要在 5 分钟内完成登录并粘回回调地址。请点「重新获取登录链接」再来一次。"
	case strings.Contains(ae.Message, "already completed"):
		return http.StatusGone, "这个登录已经完成过了。账号列表里没有新账号的话，请点「重新获取登录链接」再登录一次。"
	case ae.Status == http.StatusConflict:
		return http.StatusGone, "这次登录已经失败（" + ae.Message + "），请点「重新获取登录链接」再来一次。"
	case ae.Status == http.StatusBadRequest:
		return http.StatusBadRequest, "回调地址不对（" + ae.Message + "）：要复制登录后浏览器地址栏里 " + p.Callback + " 开头的完整地址。"
	}
	return http.StatusBadGateway, "回调失败：" + err.Error()
}
