// Package feishu is a thin client for the Feishu/Lark OpenAPI plus the
// contact-directory sync that mirrors employees and departments into the store.
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// APIError is a Feishu envelope error (non-zero code).
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("feishu: api error code=%d msg=%q", e.Code, e.Msg)
}

// apiErr builds an *APIError.
func apiErr(code int, msg string) error { return &APIError{Code: code, Msg: msg} }

// codeNoDeptAuthority is returned when a department lies outside the app's
// contact scope, e.g. the root when the scope is only some members.
const codeNoDeptAuthority = 40004

// IsNoDeptAuthority reports whether err is Feishu's "no dept authority".
func IsNoDeptAuthority(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == codeNoDeptAuthority
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

// deptItem is a department object as returned by the contact API.
type deptItem struct {
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
}

// toDept converts a live department; ok is false for deleted or ID-less ones.
func (it deptItem) toDept() (d Dept, ok bool) {
	if it.Status.IsDeleted || it.OpenDepartmentID == "" {
		return Dept{}, false
	}
	d = Dept{ID: it.OpenDepartmentID, ParentID: it.ParentDepartmentID, Name: it.Name}
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
	return d, true
}

// deptListResp is one page of "list sub-departments".
type deptListResp struct {
	envelope
	Data struct {
		Items     []deptItem `json:"items"`
		PageToken string     `json:"page_token"`
		HasMore   bool       `json:"has_more"`
	} `json:"data"`
}

// ListDepartments returns every department in the app's contact scope by
// listing the root's children recursively
// (GET /contact/v3/departments/0/children?fetch_child=true). All IDs are
// open_department_id and leaders are open_ids. When the scope is not the
// whole tenant Feishu rejects the root (see IsNoDeptAuthority); Directory
// handles that case.
func (c *Client) ListDepartments(ctx context.Context) ([]Dept, error) {
	return c.listSubDepartments(ctx, "0")
}

// listSubDepartments returns every descendant of parent (not parent itself).
func (c *Client) listSubDepartments(ctx context.Context, parent string) ([]Dept, error) {
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
			c.base+"/open-apis/contact/v3/departments/"+url.PathEscape(parent)+"/children?"+q.Encode(), token, nil, &r)
		if err != nil {
			return nil, err
		}
		if r.Code != 0 {
			return nil, apiErr(r.Code, r.Msg)
		}
		for _, it := range r.Data.Items {
			if d, ok := it.toDept(); ok {
				out = append(out, d)
			}
		}
		if !r.Data.HasMore || r.Data.PageToken == "" {
			break
		}
		pageToken = r.Data.PageToken
	}
	return out, nil
}

// GetDepartment fetches one department (GET /contact/v3/departments/:id);
// ok is false when Feishu reports it deleted.
func (c *Client) GetDepartment(ctx context.Context, id string) (d Dept, ok bool, err error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return Dept{}, false, err
	}
	q := url.Values{}
	q.Set("department_id_type", "open_department_id")
	q.Set("user_id_type", "open_id")
	var r struct {
		envelope
		Data struct {
			Department deptItem `json:"department"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet,
		c.base+"/open-apis/contact/v3/departments/"+url.PathEscape(id)+"?"+q.Encode(), token, nil, &r); err != nil {
		return Dept{}, false, err
	}
	if r.Code != 0 {
		return Dept{}, false, apiErr(r.Code, r.Msg)
	}
	d, ok = r.Data.Department.toDept()
	return d, ok, nil
}

// User is a Feishu user with its department memberships (open_department_id).
type User struct {
	OpenID, UnionID, UserID, Name, Email, AvatarURL string
	Active                                          bool
	DeptFeishuIDs                                   []string
}

// userItem is a user object as returned by the contact API.
type userItem struct {
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
}

// toUser converts a user. It is Active unless Feishu reports it resigned or
// frozen; without the status field permission every user counts as active
// (resigned users are not listed anyway).
func (it userItem) toUser() User {
	return User{
		OpenID:        it.OpenID,
		UnionID:       it.UnionID,
		UserID:        it.UserID,
		Name:          it.Name,
		Email:         it.Email,
		AvatarURL:     it.Avatar.Avatar72,
		Active:        !it.Status.IsResigned && !it.Status.IsFrozen,
		DeptFeishuIDs: it.DepartmentIDs,
	}
}

// userListResp is one page of "list a department's direct users".
type userListResp struct {
	envelope
	Data struct {
		Items     []userItem `json:"items"`
		PageToken string     `json:"page_token"`
		HasMore   bool       `json:"has_more"`
	} `json:"data"`
}

// ListUsers returns every user who is a direct member of the root department
// ("0") or of any of deptIDs (open_department_id), de-duplicated by open_id.
func (c *Client) ListUsers(ctx context.Context, deptIDs []string) ([]User, error) {
	return c.listDepartmentUsers(ctx, append([]string{"0"}, deptIDs...))
}

// listDepartmentUsers returns the direct members of exactly deptIDs,
// de-duplicated by open_id (GET /contact/v3/users/find_by_department).
func (c *Client) listDepartmentUsers(ctx context.Context, deptIDs []string) ([]User, error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []User
	seen := map[string]bool{}
	for _, dept := range deptIDs {
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
				out = append(out, it.toUser())
			}
			if !r.Data.HasMore || r.Data.PageToken == "" {
				break
			}
			pageToken = r.Data.PageToken
		}
	}
	return out, nil
}

// GetUsers fetches users by open_id, 50 per call (GET /contact/v3/users/batch).
func (c *Client) GetUsers(ctx context.Context, openIDs []string) ([]User, error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out []User
	for start := 0; start < len(openIDs); start += 50 {
		q := url.Values{}
		q["user_ids"] = openIDs[start:min(start+50, len(openIDs))]
		q.Set("user_id_type", "open_id")
		q.Set("department_id_type", "open_department_id")
		var r struct {
			envelope
			Data struct {
				Items []userItem `json:"items"`
			} `json:"data"`
		}
		if err := c.do(ctx, http.MethodGet,
			c.base+"/open-apis/contact/v3/users/batch?"+q.Encode(), token, nil, &r); err != nil {
			return nil, err
		}
		if r.Code != 0 {
			return nil, apiErr(r.Code, r.Msg)
		}
		for _, it := range r.Data.Items {
			if it.OpenID != "" {
				out = append(out, it.toUser())
			}
		}
	}
	return out, nil
}

// ContactScope returns the departments and individual members the app may
// read (GET /contact/v3/scopes), as open_department_id / open_id.
func (c *Client) ContactScope(ctx context.Context) (deptIDs, userIDs []string, err error) {
	token, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("user_id_type", "open_id")
		q.Set("department_id_type", "open_department_id")
		q.Set("page_size", "100")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var r struct {
			envelope
			Data struct {
				DepartmentIDs []string `json:"department_ids"`
				UserIDs       []string `json:"user_ids"`
				PageToken     string   `json:"page_token"`
				HasMore       bool     `json:"has_more"`
			} `json:"data"`
		}
		if err := c.do(ctx, http.MethodGet,
			c.base+"/open-apis/contact/v3/scopes?"+q.Encode(), token, nil, &r); err != nil {
			return nil, nil, err
		}
		if r.Code != 0 {
			return nil, nil, apiErr(r.Code, r.Msg)
		}
		deptIDs = append(deptIDs, r.Data.DepartmentIDs...)
		userIDs = append(userIDs, r.Data.UserIDs...)
		if !r.Data.HasMore || r.Data.PageToken == "" {
			return deptIDs, userIDs, nil
		}
		pageToken = r.Data.PageToken
	}
}

// Directory returns every department and user the app may read. A
// tenant-wide scope is listed from the root. A partial scope (some
// departments and/or individual members, as in a pilot) makes Feishu reject
// the root, so it is expanded from ContactScope instead: each granted
// department with its sub-departments and their members, plus each
// individually granted member.
func (c *Client) Directory(ctx context.Context) ([]Dept, []User, error) {
	depts, err := c.ListDepartments(ctx)
	if err == nil {
		users, err := c.ListUsers(ctx, deptIDsOf(depts))
		return depts, users, err
	}
	if !IsNoDeptAuthority(err) {
		return nil, nil, err
	}

	scopeDepts, scopeUsers, err := c.ContactScope(ctx)
	if err != nil {
		return nil, nil, err
	}
	depts = nil
	seenDept := map[string]bool{}
	add := func(d Dept) {
		if !seenDept[d.ID] {
			seenDept[d.ID] = true
			depts = append(depts, d)
		}
	}
	for _, id := range scopeDepts {
		if seenDept[id] {
			continue // already reached as a sub-department
		}
		d, ok, err := c.GetDepartment(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		add(d)
		subs, err := c.listSubDepartments(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		for _, s := range subs {
			add(s)
		}
	}
	users, err := c.listDepartmentUsers(ctx, deptIDsOf(depts))
	if err != nil {
		return nil, nil, err
	}
	have := make(map[string]bool, len(users))
	for _, u := range users {
		have[u.OpenID] = true
	}
	var missing []string
	for _, id := range scopeUsers {
		if !have[id] {
			have[id] = true
			missing = append(missing, id)
		}
	}
	extra, err := c.GetUsers(ctx, missing)
	if err != nil {
		return nil, nil, err
	}
	return depts, append(users, extra...), nil
}

func deptIDsOf(depts []Dept) []string {
	ids := make([]string, len(depts))
	for i, d := range depts {
		ids[i] = d.ID
	}
	return ids
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
