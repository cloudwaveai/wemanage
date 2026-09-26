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
		return fmt.Errorf("cpa: %s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(data))
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
	q := url.Values{}
	q.Set("state", state)
	req, reqErr := c.newRequest(ctx, http.MethodGet, managementBase+"/get-auth-status", q, nil)
	if reqErr != nil {
		return "", reqErr
	}
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	switch out.Status {
	case "ok":
		return "ok", nil
	case "error":
		return "error", nil
	default:
		return "pending", nil
	}
}
