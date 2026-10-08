package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/store"
)

// ChatGPT logins are done by the gateway itself, the way the official Codex
// CLI does them now, rather than by CPA: since Codex moved its login callback
// to 127.0.0.1 (openai/codex#47927, 2026-09-24), logins with CPA's link
// (redirect localhost) fail on OpenAI's password page. The gateway makes the
// authorize link (Codex's client, callback, scopes and originator, with its
// own state and PKCE), exchanges the pasted callback's code for tokens, and
// hands CPA the credential in CPA's own file format. CPA then refreshes and
// uses it as if it had logged in itself.

const (
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRedirectURI  = "http://127.0.0.1:1455/auth/callback"
	codexScopes       = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	codexOriginator   = "codex_cli_rs"
	codexLoginTTL     = 30 * time.Minute
	defaultCodexIssue = "https://auth.openai.com"
)

// codexLogins keeps each started login's PKCE verifier until it is used.
type codexLogins struct {
	mu sync.Mutex
	m  map[string]codexPending
}

type codexPending struct {
	verifier string
	expires  time.Time
}

func (c *codexLogins) put(state, verifier string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]codexPending{}
	}
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	c.m[state] = codexPending{verifier: verifier, expires: now.Add(codexLoginTTL)}
}

// take returns the verifier for state once; later calls, and expired logins, miss.
func (c *codexLogins) take(state string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[state]
	delete(c.m, state)
	return p.verifier, ok && time.Now().Before(p.expires)
}

func (s *Server) codexIssuer() string {
	if s.deps.CodexAuthBase != "" {
		return strings.TrimRight(s.deps.CodexAuthBase, "/")
	}
	return defaultCodexIssue
}

// startCodexLogin makes the authorize link and records the onboarding.
func (s *Server) startCodexLogin(w http.ResponseWriter, r *http.Request, p loginProvider, ipGroupID int64, os store.DeviceProfile) {
	state := secure.RandomToken(24)
	verifier := secure.RandomToken(48) // 64 characters, within PKCE's 43..128
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":                  {codexClientID},
		"response_type":              {"code"},
		"redirect_uri":               {codexRedirectURI},
		"scope":                      {codexScopes},
		"state":                      {state},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {codexOriginator},
	}
	authURL := s.codexIssuer() + "/oauth/authorize?" + q.Encode()
	emp := currentEmployee(r)
	if err := s.deps.Store.CreateOnboarding(r.Context(), store.Onboarding{
		State: state, IPGroupID: ipGroupID, OSProfile: os, KnownAuthNames: []string{}, CreatedBy: &emp.ID, Provider: p.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot record onboarding")
		return
	}
	s.codex.put(state, verifier)
	s.audit(r, "account_login_start", "ip_group", fmt.Sprint(ipGroupID), map[string]any{"provider": p.ID})
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": p.ID, "auth_url": authURL, "state": state,
		"expires_in": int(codexLoginTTL.Seconds()), "paste": true, "callback": codexRedirectURI,
	})
}

// completeCodexLogin exchanges the pasted callback for tokens and gives the
// credential to CPA. Logging in again with an account the gateway already
// has refreshes that account's credential instead of adding a second one.
func (s *Server) completeCodexLogin(w http.ResponseWriter, r *http.Request, ob store.Onboarding, redirect string) {
	ctx := r.Context()
	u, err := url.Parse(strings.TrimSpace(redirect))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "回调地址不对：要复制登录后浏览器地址栏里 "+codexRedirectURI+" 开头的完整地址。")
		return
	}
	cb := u.Query()
	if e := cb.Get("error"); e != "" {
		writeErr(w, http.StatusGone, "OpenAI 没有完成授权（"+e+" "+cb.Get("error_description")+"）。请点「重新获取登录链接」再来一次。")
		return
	}
	code := cb.Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "回调地址里没有授权码：要复制登录后浏览器地址栏里 "+codexRedirectURI+" 开头的完整地址。")
		return
	}
	verifier, ok := s.codex.take(ob.State)
	if !ok {
		writeErr(w, http.StatusGone, "这次登录已经失效（超过 30 分钟，或网关重启过）。请点「重新获取登录链接」再来一次。")
		return
	}
	tok, err := s.exchangeCodexCode(ctx, code, verifier)
	if err != nil {
		writeErr(w, http.StatusGone, "用授权码换取登录凭证失败（"+err.Error()+"）。授权码只能用一次、几分钟内有效，请点「重新获取登录链接」再登录一次。")
		return
	}

	// The same ChatGPT account already in CPA keeps its file name, so the new
	// credential replaces the old one.
	name := ""
	if files, err := s.deps.CPA.ListAuthFiles(ctx); err == nil {
		for _, f := range files {
			if f.Type == store.ProviderCodex && tok.accountID != "" && f.IDToken.ChatGPTAccountID == tok.accountID {
				name = f.Name
				break
			}
		}
	}
	if name == "" {
		name = "codex-" + fileSafe(tok.email) + "-" + secure.RandomToken(4) + ".json"
	}
	content, _ := json.Marshal(map[string]any{
		"type": "codex", "email": tok.email, "account_id": tok.accountID,
		"id_token": tok.idToken, "access_token": tok.accessToken, "refresh_token": tok.refreshToken,
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
		"expired":      time.Now().Add(time.Duration(tok.expiresIn) * time.Second).UTC().Format(time.RFC3339),
	})
	if err := s.deps.CPA.UploadAuthFile(ctx, name, content); err != nil {
		writeErr(w, http.StatusBadGateway, "登录成功了，但凭证没能写进 CPA："+err.Error())
		return
	}

	if acc, err := s.deps.Store.GetAccountByCPAName(ctx, name); err == nil {
		_ = s.deps.Store.CompleteOnboarding(ctx, ob.State, acc.ID)
		s.audit(r, "account_relogin", "account", fmt.Sprint(acc.ID), map[string]any{"email": tok.email, "via": "codex"})
		writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "email": tok.email, "provider": store.ProviderCodex, "updated": true})
		return
	}
	acc, err := s.finishAccount(r, ob, name, tok.email, store.ProviderCodex)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.CompleteOnboarding(ctx, ob.State, acc.ID)
	s.audit(r, "account_added", "account", fmt.Sprint(acc.ID), map[string]any{"email": tok.email, "via": "codex", "cpa_name": name})
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "email": tok.email, "provider": store.ProviderCodex})
}

type codexTokens struct {
	idToken, accessToken, refreshToken string
	expiresIn                          int
	email, accountID                   string
}

var codexHTTP = &http.Client{Timeout: 30 * time.Second}

// exchangeCodexCode trades an authorization code for tokens at OpenAI.
func (s *Server) exchangeCodexCode(ctx context.Context, code, verifier string) (codexTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"redirect_uri":  {codexRedirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.codexIssuer()+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return codexTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := codexHTTP.Do(req)
	if err != nil {
		return codexTokens{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       any    `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		msg := strings.TrimSpace(fmt.Sprint(e.Error, " ", e.Description))
		if e.Error == nil {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return codexTokens{}, errors.New(msg)
	}
	var t struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" || t.RefreshToken == "" {
		return codexTokens{}, errors.New("OpenAI 返回的凭证不完整")
	}
	out := codexTokens{idToken: t.IDToken, accessToken: t.AccessToken, refreshToken: t.RefreshToken, expiresIn: t.ExpiresIn}
	if out.expiresIn <= 0 {
		out.expiresIn = 3600
	}
	out.email, out.accountID = codexIdentity(t.IDToken)
	return out, nil
}

// codexIdentity reads the email and ChatGPT account id from an id_token's
// claims (no signature check: it came straight from OpenAI over TLS).
func codexIdentity(idToken string) (email, accountID string) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", ""
	}
	var c struct {
		Email string `json:"email"`
		Auth  struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Email, c.Auth.AccountID
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._@-]+`)

func fileSafe(s string) string {
	s = unsafeFileChars.ReplaceAllString(s, "_")
	if s == "" {
		return "account"
	}
	return s
}
