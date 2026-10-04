// Package cpa wraps the CLIProxyAPI (CPA) management API.
//
// Every management request targets the base path /v0/management and carries the
// header "Authorization: Bearer <managementKey>". On any non-2xx response the
// returned error includes both the HTTP status code and the raw response body.
package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"aiseat/internal/store"
)

// managementBase is the shared path prefix for all CPA management endpoints.
const managementBase = "/v0/management"

// Client talks to a single CPA instance's management API.
type Client struct {
	baseURL string // scheme+host, no trailing slash
	key     string // management bearer key
	hc      *http.Client
}

// NewClient builds a Client. A nil hc yields a default client with a 30s timeout.
func NewClient(baseURL, managementKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	// Trim any trailing slashes so path concatenation stays clean.
	for len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}
	return &Client{baseURL: baseURL, key: managementKey, hc: hc}
}

// AuthFile is one CPA auth-file entry as returned by the list endpoint.
// NOTE: CPA's list response does NOT expose the file's prefix or proxy_url, so
// those fields are absent here (see sync.go for the reconciliation limitation).
type AuthFile struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
	// AuthIndex identifies the credential in api-call requests.
	AuthIndex string `json:"auth_index"`
	// IDToken holds claims CPA parses from a Codex id_token.
	IDToken struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		PlanType         string `json:"plan_type"`
	} `json:"id_token"`
}

// APICallRequest is a request CPA makes upstream on a credential's behalf
// (POST /v0/management/api-call). "$TOKEN$" in a header value is replaced
// with the credential's access token, and the credential's proxy_url is used,
// so the call leaves through the account's own egress.
type APICallRequest struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header,omitempty"`
	Data      string            `json:"data,omitempty"`
}

// APICallResponse is the upstream response relayed by api-call.
type APICallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

// newRequest builds an authenticated management request. If body is a []byte it
// is sent verbatim; any other non-nil body is JSON-encoded and the Content-Type
// header is set accordingly.
func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	var reader io.Reader
	jsonBody := false
	if body != nil {
		switch b := body.(type) {
		case []byte:
			reader = bytes.NewReader(b)
		default:
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("cpa: encode %s body: %w", path, err)
			}
			reader = bytes.NewReader(data)
			jsonBody = true
		}
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do executes req, enforces a 2xx status, and (when out != nil) decodes the JSON
// response body into out.
// APIError is a non-2xx answer from CPA; Message is its "error" field.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
	Body         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cpa: %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cpa: read %s response: %w", req.URL.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &APIError{Method: req.Method, Path: req.URL.Path, Status: resp.StatusCode, Body: string(data)}
		var body struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &body) == nil {
			e.Message = body.Error
		}
		return e
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("cpa: decode %s response: %w", req.URL.Path, err)
		}
	}
	return nil
}

// ListAuthFiles returns all auth files known to CPA.
func (c *Client) ListAuthFiles(ctx context.Context) ([]AuthFile, error) {
	req, err := c.newRequest(ctx, http.MethodGet, managementBase+"/auth-files", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		ObservedAt string     `json:"observed_at"`
		Files      []AuthFile `json:"files"`
	}
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// APICall asks CPA to make an upstream request with a credential.
func (c *Client) APICall(ctx context.Context, call APICallRequest) (APICallResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, managementBase+"/api-call", nil, call)
	if err != nil {
		return APICallResponse{}, err
	}
	var out APICallResponse
	if err := c.do(req, &out); err != nil {
		return APICallResponse{}, err
	}
	return out, nil
}

// StaticModels returns CPA's built-in model catalogue for a channel
// (GET /v0/management/model-definitions/{channel}), e.g. "codex" or "kimi".
func (c *Client) StaticModels(ctx context.Context, channel string) ([]store.ModelInfo, error) {
	req, err := c.newRequest(ctx, http.MethodGet, managementBase+"/model-definitions/"+url.PathEscape(channel), nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Models []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Created     int64  `json:"created"`
		} `json:"models"`
	}
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	models := make([]store.ModelInfo, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, store.ModelInfo{ID: m.ID, Name: m.DisplayName, Created: m.Created})
	}
	return models, nil
}

// UploadAuthFile uploads a new auth file.
//
// Body-format choice: we POST JSON {"name": name, "content": string(content)} to
// /v0/management/auth-files. The raw file bytes are sent as a UTF-8 JSON string
// because CPA auth files are JSON text; if binary auth material ever needs
// uploading, switch "content" to a base64-encoded string. This is the primary
// format suggested by the spec (rather than the raw-body + X-Auth-File-Name
// fallback), since a single JSON body is unambiguous and easy to validate.
func (c *Client) UploadAuthFile(ctx context.Context, name string, content []byte) error {
	body := map[string]any{
		"name":    name,
		"content": string(content),
	}
	req, err := c.newRequest(ctx, http.MethodPost, managementBase+"/auth-files", nil, body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// DeleteAuthFile removes an auth file by name.
func (c *Client) DeleteAuthFile(ctx context.Context, name string) error {
	q := url.Values{}
	q.Set("name", name)
	req, err := c.newRequest(ctx, http.MethodDelete, managementBase+"/auth-files", q, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// PatchFields updates mutable fields (e.g. prefix, proxy_url) of a named auth
// file. The request body is the caller-supplied fields plus {"name": name}.
func (c *Client) PatchFields(ctx context.Context, name string, fields map[string]any) error {
	body := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		body[k] = v
	}
	body["name"] = name
	req, err := c.newRequest(ctx, http.MethodPatch, managementBase+"/auth-files/fields", nil, body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// RequestCodexAuthURL requests a Codex OAuth authorization URL and its state.
// RequestKimiAuthURL starts a Kimi Code device-code login. The operator opens
// url (and, if given, enters userCode); CPA polls in the background and saves
// the credential, after which AuthStatus(state) reports "ok".
func (c *Client) RequestKimiAuthURL(ctx context.Context) (url, state, userCode string, err error) {
	req, reqErr := c.newRequest(ctx, http.MethodGet, managementBase+"/kimi-auth-url", nil, nil)
	if reqErr != nil {
		return "", "", "", reqErr
	}
	var out struct {
		URL      string `json:"url"`
		State    string `json:"state"`
		UserCode string `json:"user_code"`
	}
	if err := c.do(req, &out); err != nil {
		return "", "", "", err
	}
	return out.URL, out.State, out.UserCode, nil
}

// LoginStart is CPA's answer to starting an account login.
type LoginStart struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	UserCode  string `json:"user_code"`  // device flows that ask for a code
	ExpiresIn int    `json:"expires_in"` // device flows: seconds the code lives
	Flow      string `json:"flow"`       // "device" for device-code logins
}

// StartLogin starts a login at one of CPA's "<provider>-auth-url" paths
// (e.g. "/xai-auth-url"); finish it with PostOAuthCallback (redirect flows)
// or by polling AuthStatusDetail (device flows, which CPA completes itself).
func (c *Client) StartLogin(ctx context.Context, path string) (LoginStart, error) {
	req, err := c.newRequest(ctx, http.MethodGet, managementBase+path, nil, nil)
	if err != nil {
		return LoginStart{}, err
	}
	var out LoginStart
	if err := c.do(req, &out); err != nil {
		return LoginStart{}, err
	}
	return out, nil
}

func (c *Client) RequestCodexAuthURL(ctx context.Context) (url, state string, err error) {
	req, reqErr := c.newRequest(ctx, http.MethodGet, managementBase+"/codex-auth-url", nil, nil)
	if reqErr != nil {
		return "", "", reqErr
	}
	var out struct {
		Status string `json:"status"`
		URL    string `json:"url"`
		State  string `json:"state"`
	}
	if err := c.do(req, &out); err != nil {
		return "", "", err
	}
	return out.URL, out.State, nil
}

// PostOAuthCallback delivers the OAuth redirect URL back to CPA to complete a
// login. It returns nil on a 2xx response (e.g. {"status":"ok"}).
func (c *Client) PostOAuthCallback(ctx context.Context, provider, redirectURL string) error {
	body := map[string]any{
		"provider":     provider,
		"redirect_url": redirectURL,
	}
	req, err := c.newRequest(ctx, http.MethodPost, managementBase+"/oauth-callback", nil, body)
	if err != nil {
		return err
	}
	var out struct {
		Status string `json:"status"`
	}
	return c.do(req, &out)
}

// AuthStatus polls the status of an in-flight auth flow identified by state.
// It normalizes the CPA response to "ok", "error", or "pending"; any status
// other than "ok"/"error" (including "pending") maps to "pending".
func (c *Client) AuthStatus(ctx context.Context, state string) (status string, err error) {
	status, _, err = c.AuthStatusDetail(ctx, state)
	return status, err
}

// AuthStatusDetail is AuthStatus plus CPA's error message when the flow failed.
func (c *Client) AuthStatusDetail(ctx context.Context, state string) (status, message string, err error) {
	q := url.Values{}
	q.Set("state", state)
	req, reqErr := c.newRequest(ctx, http.MethodGet, managementBase+"/get-auth-status", q, nil)
	if reqErr != nil {
		return "", "", reqErr
	}
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.do(req, &out); err != nil {
		return "", "", err
	}
	switch out.Status {
	case "ok":
		return "ok", "", nil
	case "error":
		return "error", out.Error, nil
	default:
		return "pending", "", nil
	}
}
