package feishu_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"aiseat/internal/feishu"
)

// contains reports whether s contains substr (avoids the strings package).
func contains(s, substr string) bool { return bytes.Contains([]byte(s), []byte(substr)) }

// hasPrefix reports whether s starts with prefix (avoids the strings package).
func hasPrefix(s, prefix string) bool { return bytes.HasPrefix([]byte(s), []byte(prefix)) }

// counter is a tiny race-safe request tally keyed by an arbitrary label.
type counter struct {
	mu sync.Mutex
	n  map[string]int
}

func newCounter() *counter { return &counter{n: make(map[string]int)} }

func (c *counter) inc(k string) {
	c.mu.Lock()
	c.n[k]++
	c.mu.Unlock()
}

func (c *counter) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

// tokenHandler registers the tenant-token route and counts hits.
func tokenHandler(t *testing.T, mux *http.ServeMux, c *counter) {
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal",
		func(w http.ResponseWriter, r *http.Request) {
			c.inc("token")
			writeJSON(t, w, map[string]any{
				"code":                0,
				"tenant_access_token": "tok-abc",
				"expire":              7200,
			})
		})
}

func TestTenantAccessTokenCaches(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	ctx := context.Background()

	tok1, err := cl.TenantAccessToken(ctx)
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	tok2, err := cl.TenantAccessToken(ctx)
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if tok1 != "tok-abc" || tok2 != "tok-abc" {
		t.Fatalf("token = %q/%q, want tok-abc", tok1, tok2)
	}
	if got := c.get("token"); got != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (cache miss)", got)
	}
}

func TestExchangeUser(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/authen/v1/oidc/access_token",
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] != "authorization_code" || body["code"] != "the-code" {
				t.Errorf("unexpected exchange body: %v", body)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tok-abc" {
				t.Errorf("Authorization = %q, want Bearer tok-abc", got)
			}
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"open_id":    "ou_alice",
					"union_id":   "on_alice",
					"user_id":    "u_alice",
					"name":       "Alice",
					"email":      "alice@example.com",
					"avatar_url": "http://img/alice.png",
				},
			})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	info, err := cl.ExchangeUser(context.Background(), "the-code", "http://cb")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if info.OpenID != "ou_alice" || info.Name != "Alice" {
		t.Fatalf("info = %+v, want open_id ou_alice / name Alice", info)
	}
	if info.Email != "alice@example.com" || info.AvatarURL != "http://img/alice.png" {
		t.Fatalf("info = %+v, unexpected email/avatar", info)
	}
}

func TestListDepartmentsPaginate(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/departments",
		func(w http.ResponseWriter, r *http.Request) {
			c.inc("depts")
			switch r.URL.Query().Get("page_token") {
			case "":
				writeJSON(t, w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"items": []map[string]any{
							{
								"department_id":        "od-1",
								"parent_department_id": "0",
								"name":                 "Root",
								"leaders": []map[string]any{
									{"leader_type": 1, "leader_id": "ou_leader"},
								},
							},
						},
						"page_token": "PAGE2",
						"has_more":   true,
					},
				})
			case "PAGE2":
				writeJSON(t, w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"items": []map[string]any{
							{"department_id": "od-2", "parent_department_id": "od-1", "name": "Eng"},
						},
						"page_token": "",
						"has_more":   false,
					},
				})
			default:
				t.Errorf("unexpected page_token %q", r.URL.Query().Get("page_token"))
			}
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	depts, err := cl.ListDepartments(context.Background())
	if err != nil {
		t.Fatalf("list departments: %v", err)
	}
	if len(depts) != 2 {
		t.Fatalf("got %d departments, want 2", len(depts))
	}
	if c.get("depts") != 2 {
		t.Fatalf("departments endpoint hit %d times, want 2 pages", c.get("depts"))
	}
	if depts[0].ID != "od-1" || depts[0].Name != "Root" {
		t.Fatalf("dept[0] = %+v", depts[0])
	}
	if len(depts[0].LeaderOpenIDs) != 1 || depts[0].LeaderOpenIDs[0] != "ou_leader" {
		t.Fatalf("dept[0] leaders = %v", depts[0].LeaderOpenIDs)
	}
	if depts[1].ID != "od-2" || depts[1].ParentID != "od-1" {
		t.Fatalf("dept[1] = %+v", depts[1])
	}
}

func TestListUsersPaginate(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/users",
		func(w http.ResponseWriter, r *http.Request) {
			c.inc("users")
			switch r.URL.Query().Get("page_token") {
			case "":
				writeJSON(t, w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"items": []map[string]any{
							{
								"open_id":        "ou_a",
								"union_id":       "on_a",
								"user_id":        "u_a",
								"name":           "Alice",
								"email":          "a@x.com",
								"avatar":         "http://img/a.png",
								"status":         map[string]any{"is_activated": true},
								"department_ids": []string{"od-2"},
							},
						},
						"page_token": "P2",
						"has_more":   true,
					},
				})
			case "P2":
				writeJSON(t, w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"items": []map[string]any{
							{
								"open_id":        "ou_b",
								"name":           "Bob",
								"status":         map[string]any{"is_activated": false},
								"department_ids": []string{},
							},
						},
						"page_token": "",
						"has_more":   false,
					},
				})
			default:
				t.Errorf("unexpected page_token %q", r.URL.Query().Get("page_token"))
			}
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	users, err := cl.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if c.get("users") != 2 {
		t.Fatalf("users endpoint hit %d times, want 2 pages", c.get("users"))
	}
	if users[0].OpenID != "ou_a" || !users[0].Active {
		t.Fatalf("user[0] = %+v, want active ou_a", users[0])
	}
	if len(users[0].DeptFeishuIDs) != 1 || users[0].DeptFeishuIDs[0] != "od-2" {
		t.Fatalf("user[0] depts = %v", users[0].DeptFeishuIDs)
	}
	if users[1].OpenID != "ou_b" || users[1].Active {
		t.Fatalf("user[1] = %+v, want inactive ou_b", users[1])
	}
}

func TestAPIErrorNonZeroCode(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/departments",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"code": 99991663, "msg": "tenant access token invalid"})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	_, err := cl.ListDepartments(context.Background())
	if err == nil {
		t.Fatal("expected error on non-zero code, got nil")
	}
	if !contains(err.Error(), "99991663") || !contains(err.Error(), "tenant access token invalid") {
		t.Fatalf("error = %q, want code and msg", err.Error())
	}
}

func TestOAuthURL(t *testing.T) {
	cl := feishu.NewClient("https://open.feishu.cn", "cli_app", "secret", http.DefaultClient)
	got := cl.OAuthURL("st ate", "https://cb/x?y=1")
	if !hasPrefix(got, "https://open.feishu.cn/open-apis/authen/v1/authorize?") {
		t.Fatalf("url prefix wrong: %q", got)
	}
	if !contains(got, "app_id=cli_app") ||
		!contains(got, "state=st+ate") ||
		!contains(got, "redirect_uri=https%3A%2F%2Fcb%2Fx%3Fy%3D1") {
		t.Fatalf("url missing encoded params: %q", got)
	}
}
