// Package feishu is a thin client for the Feishu/Lark OpenAPI plus the
// contact-directory sync that mirrors employees and departments into the store.
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultBase is the public Feishu OpenAPI host.
const defaultBase = "https://open.feishu.cn"

// Client talks to the Feishu OpenAPI. The tenant access token is cached in
// memory and refreshed shortly before it expires.
type Client struct {
	base      string // OpenAPI host, e.g. https://open.feishu.cn
	authBase  string // OAuth authorize host, e.g. https://accounts.feishu.cn
	appID     string
	appSecret string
	hc        *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewClient builds a Client. An empty base defaults to the public host; a nil
// http.Client gets a sane default with a timeout.
func NewClient(base, appID, appSecret string, hc *http.Client) *Client {
	if base == "" {
		base = defaultBase
	}
	base = trimTrailingSlash(base)
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	// The OAuth authorize endpoint lives on the accounts.* host, not the API host.
	authBase := "https://accounts.feishu.cn"
	if strings.Contains(base, "larksuite") {
		authBase = "https://accounts.larksuite.com"
	}
	return &Client{base: base, authBase: authBase, appID: appID, appSecret: appSecret, hc: hc}
}

// SetAuthBase overrides the OAuth authorize host (used in tests).
func (c *Client) SetAuthBase(u string) { c.authBase = trimTrailingSlash(u) }

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// apiErr formats a Feishu envelope error (non-zero code).
func apiErr(code int, msg string) error {
	return fmt.Errorf("feishu: api error code=%d msg=%q", code, msg)
}

// envelope is the common {code,msg} prefix returned by every endpoint.
type envelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// do issues an HTTP request, JSON-encoding body when non-nil and decoding the
// response into out. token, when non-empty, is sent as a bearer credential.
func (c *Client) do(ctx context.Context, method, fullURL, token string, body, out any) error {
	var req *http.Request
	var err error
	if body != nil {
		b, mErr := json.Marshal(body)
		if mErr != nil {
			return mErr
		}
		req, err = http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(b))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, fullURL, http.NoBody)
	}
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("feishu: http status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// tokenResp is the tenant_access_token response.
type tokenResp struct {
	envelope
	Token  string `json:"tenant_access_token"`
	Expire int    `json:"expire"`
}

// TenantAccessToken returns a cached tenant access token, fetching a fresh one
// only when the cache is empty or within 5 minutes of expiry.
func (c *Client) TenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	var r tokenResp
	err := c.do(ctx, http.MethodPost,
		c.base+"/open-apis/auth/v3/tenant_access_token/internal", "",
		map[string]string{"app_id": c.appID, "app_secret": c.appSecret}, &r)
	if err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", apiErr(r.Code, r.Msg)
	}
	expire := r.Expire
	if expire <= 0 {
		expire = 7200
	}
	c.token = r.Token
	c.tokenExp = time.Now().Add(time.Duration(expire)*time.Second - 5*time.Minute)
	return c.token, nil
}

// UserInfo is the identity resolved from an OAuth login exchange.
type UserInfo struct {
	OpenID, UnionID, UserID, Name, Email, AvatarURL string
}

// OAuthURL builds the authorization URL the browser is redirected to
// (Feishu OAuth 2.0: https://accounts.feishu.cn/open-apis/authen/v1/authorize).
// An empty scope relies on the app's platform-configured permissions.
func (c *Client) OAuthURL(state, redirectURI, scope string) string {
	q := url.Values{}
	q.Set("client_id", c.appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	if scope != "" {
		q.Set("scope", scope)
	}
	return c.authBase + "/open-apis/authen/v1/authorize?" + q.Encode()
}

// oauthTokenResp is the authen/v2/oauth/token response. Feishu returns a flat
// OAuth2 object; parsed defensively to tolerate an envelope or error body.
type oauthTokenResp struct {
	Code             int    `json:"code"`
	Msg              string `json:"msg"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	AccessToken      string `json:"access_token"`
}

// userInfoResp is the authen/v1/user_info response.
type userInfoResp struct {
	envelope
	Data struct {
		OpenID    string `json:"open_id"`
		UnionID   string `json:"union_id"`
		UserID    string `json:"user_id"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	} `json:"data"`
}

// ExchangeUser exchanges an authorization code for the caller's identity:
// authen/v2/oauth/token (code -> user access token) then authen/v1/user_info.
func (c *Client) ExchangeUser(ctx context.Context, code, redirectURI string) (UserInfo, error) {
	var tok oauthTokenResp
	err := c.do(ctx, http.MethodPost,
		c.base+"/open-apis/authen/v2/oauth/token", "",
		map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     c.appID,
			"client_secret": c.appSecret,
			"code":          code,
			"redirect_uri":  redirectURI,
		}, &tok)
	if err != nil {
		return UserInfo{}, err
	}
	if tok.AccessToken == "" {
		msg := tok.Msg
		if tok.Error != "" {
			msg = tok.Error + ": " + tok.ErrorDescription
		}
		return UserInfo{}, apiErr(tok.Code, msg)
	}

	var info userInfoResp
	if err := c.do(ctx, http.MethodGet,
		c.base+"/open-apis/authen/v1/user_info", tok.AccessToken, nil, &info); err != nil {
		return UserInfo{}, err
	}
	if info.Code != 0 {
		return UserInfo{}, apiErr(info.Code, info.Msg)
	}
	if info.Data.OpenID == "" {
		return UserInfo{}, fmt.Errorf("feishu: user_info returned no open_id (check app permissions)")
	}
	return UserInfo{
		OpenID:    info.Data.OpenID,
		UnionID:   info.Data.UnionID,
		UserID:    info.Data.UserID,
		Name:      info.Data.Name,
		Email:     info.Data.Email,
		AvatarURL: info.Data.AvatarURL,
	}, nil
}

// Dept is a Feishu department. IDs are open_department_id values.
type Dept struct {
	ID, ParentID, Name string
	LeaderOpenIDs      []string
}

// deptListResp is one page of "list sub-departments".
type deptListResp struct {
	envelope
	Data struct {
		Items []struct {
			OpenDepartmentID   string `json:"open_department_id"`
			ParentDepartmentID string `json:"parent_department_id"`
			Name               string `json:"name"`
			LeaderUserID       string `json:"leader_user_id"`
			Leaders            []struct {
				LeaderType int    `json:"leader_type"`
				LeaderID   string `json:"leader_id"`
			} `json:"leaders"`
			Status struct {
				IsDeleted bool `json:"is_deleted"`
			} `json:"status"`
		} `json:"items"`
		PageToken string `json:"page_token"`
		HasMore   bool   `json:"has_more"`
	} `json:"data"`
}

// ListDepartments returns every department in the app's contact scope by
// listing the root's children recursively
// (GET /contact/v3/departments/0/children?fetch_child=true). All IDs are
// open_department_id and leaders are open_ids.
func (c *Client) ListDepartments(ctx context.Context) ([]Dept, error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []Dept
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("fetch_child", "true")
		q.Set("department_id_type", "open_department_id")
		q.Set("user_id_type", "open_id")
		q.Set("page_size", "50")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var r deptListResp
		err := c.do(ctx, http.MethodGet,
			c.base+"/open-apis/contact/v3/departments/0/children?"+q.Encode(), token, nil, &r)
		if err != nil {
			return nil, err
		}
		if r.Code != 0 {
			return nil, apiErr(r.Code, r.Msg)
		}
		for _, it := range r.Data.Items {
			if it.Status.IsDeleted || it.OpenDepartmentID == "" {
				continue
			}
			d := Dept{ID: it.OpenDepartmentID, ParentID: it.ParentDepartmentID, Name: it.Name}
			seen := map[string]bool{}
			for _, l := range it.Leaders {
				if l.LeaderID != "" && !seen[l.LeaderID] {
					seen[l.LeaderID] = true
					d.LeaderOpenIDs = append(d.LeaderOpenIDs, l.LeaderID)
				}
			}
			if it.LeaderUserID != "" && !seen[it.LeaderUserID] {
				d.LeaderOpenIDs = append(d.LeaderOpenIDs, it.LeaderUserID)
			}
			out = append(out, d)
		}
		if !r.Data.HasMore || r.Data.PageToken == "" {
			break
		}
		pageToken = r.Data.PageToken
	}
	return out, nil
}

// User is a Feishu user with its department memberships (open_department_id).
type User struct {
	OpenID, UnionID, UserID, Name, Email, AvatarURL string
	Active                                          bool
	DeptFeishuIDs                                   []string
}

// userListResp is one page of "list a department's direct users".
type userListResp struct {
	envelope
	Data struct {
		Items []struct {
			OpenID  string `json:"open_id"`
			UnionID string `json:"union_id"`
			UserID  string `json:"user_id"`
			Name    string `json:"name"`
			Email   string `json:"email"`
			Avatar  struct {
				Avatar72 string `json:"avatar_72"`
			} `json:"avatar"`
			Status struct {
				IsResigned bool `json:"is_resigned"`
				IsFrozen   bool `json:"is_frozen"`
			} `json:"status"`
			DepartmentIDs []string `json:"department_ids"`
		} `json:"items"`
		PageToken string `json:"page_token"`
		HasMore   bool   `json:"has_more"`
	} `json:"data"`
}

// ListUsers returns every user who is a direct member of the root department
// ("0") or of any of deptIDs (open_department_id), de-duplicated by open_id
// (GET /contact/v3/users/find_by_department). A user is Active unless Feishu
// reports them resigned or frozen; without the status field permission every
// returned user counts as active (resigned users are not listed anyway).
func (c *Client) ListUsers(ctx context.Context, deptIDs []string) ([]User, error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []User
	seen := map[string]bool{}
	for _, dept := range append([]string{"0"}, deptIDs...) {
		pageToken := ""
		for {
			q := url.Values{}
			q.Set("department_id", dept)
			q.Set("department_id_type", "open_department_id")
			q.Set("user_id_type", "open_id")
			q.Set("page_size", "50")
			if pageToken != "" {
				q.Set("page_token", pageToken)
			}
			var r userListResp
			err := c.do(ctx, http.MethodGet,
				c.base+"/open-apis/contact/v3/users/find_by_department?"+q.Encode(), token, nil, &r)
			if err != nil {
				return nil, err
			}
			if r.Code != 0 {
				return nil, apiErr(r.Code, r.Msg)
			}
			for _, it := range r.Data.Items {
				if it.OpenID == "" || seen[it.OpenID] {
					continue
				}
				seen[it.OpenID] = true
				out = append(out, User{
					OpenID:        it.OpenID,
					UnionID:       it.UnionID,
					UserID:        it.UserID,
					Name:          it.Name,
					Email:         it.Email,
					AvatarURL:     it.Avatar.Avatar72,
					Active:        !it.Status.IsResigned && !it.Status.IsFrozen,
					DeptFeishuIDs: it.DepartmentIDs,
				})
			}
			if !r.Data.HasMore || r.Data.PageToken == "" {
				break
			}
			pageToken = r.Data.PageToken
		}
	}
	return out, nil
}

// sendText/sendCard share this message-send helper.
func (c *Client) sendMessage(ctx context.Context, openID, msgType, content string) error {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return err
	}
	var r envelope
	err = c.do(ctx, http.MethodPost,
		c.base+"/open-apis/im/v1/messages?receive_id_type=open_id", token,
		map[string]string{"receive_id": openID, "msg_type": msgType, "content": content}, &r)
	if err != nil {
		return err
	}
	if r.Code != 0 {
		return apiErr(r.Code, r.Msg)
	}
	return nil
}

// SendText sends a plain-text IM message to an open_id.
func (c *Client) SendText(ctx context.Context, openID, text string) error {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return c.sendMessage(ctx, openID, "text", string(content))
}

// SendCard sends an interactive card, marshaled to the JSON string content.
func (c *Client) SendCard(ctx context.Context, openID string, card any) error {
	content, err := json.Marshal(card)
	if err != nil {
		return err
	}
	return c.sendMessage(ctx, openID, "interactive", string(content))
}
