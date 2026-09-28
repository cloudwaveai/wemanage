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
	mux := http.NewServeMux()
	// Step 1: code -> user access token (v2 oauth/token, flat OAuth2 response).
	mux.HandleFunc("/open-apis/authen/v2/oauth/token",
		func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] != "authorization_code" || body["code"] != "the-code" {
				t.Errorf("unexpected token body: %v", body)
			}
			if body["client_id"] != "app" || body["client_secret"] != "secret" {
				t.Errorf("client creds not sent: %v", body)
			}
			if body["redirect_uri"] != "http://cb" {
				t.Errorf("redirect_uri = %q", body["redirect_uri"])
			}
			writeJSON(t, w, map[string]any{"code": 0, "access_token": "u-alice-token", "token_type": "Bearer", "expires_in": 7200})
		})
	// Step 2: user access token -> profile (v1 user_info).
	mux.HandleFunc("/open-apis/authen/v1/user_info",
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer u-alice-token" {
				t.Errorf("Authorization = %q, want Bearer u-alice-token", got)
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
	mux.HandleFunc("/open-apis/contact/v3/departments/0/children",
		func(w http.ResponseWriter, r *http.Request) {
			c.inc("depts")
			q := r.URL.Query()
			if q.Get("fetch_child") != "true" || q.Get("department_id_type") != "open_department_id" {
				t.Errorf("query = %v, want recursive open_department_id listing", q)
			}
			switch q.Get("page_token") {
			case "":
				writeJSON(t, w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"items": []map[string]any{
							{
								"open_department_id":   "od-1",
								"department_id":        "custom-1",
								"parent_department_id": "0",
								"name":                 "Root",
								"leader_user_id":       "ou_leader",
								"leaders": []map[string]any{
									{"leader_type": 1, "leader_id": "ou_leader"},
									{"leader_type": 2, "leader_id": "ou_deputy"},
								},
							},
							{"open_department_id": "od-gone", "name": "Deleted", "status": map[string]any{"is_deleted": true}},
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
							{"open_department_id": "od-2", "parent_department_id": "od-1", "name": "Eng"},
						},
						"page_token": "",
						"has_more":   false,
					},
				})
			default:
				t.Errorf("unexpected page_token %q", q.Get("page_token"))
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
		t.Fatalf("got %d departments, want 2 (deleted one skipped)", len(depts))
	}
	if c.get("depts") != 2 {
		t.Fatalf("departments endpoint hit %d times, want 2 pages", c.get("depts"))
	}
	if depts[0].ID != "od-1" || depts[0].Name != "Root" {
		t.Fatalf("dept[0] = %+v, want open_department_id od-1", depts[0])
	}
	if len(depts[0].LeaderOpenIDs) != 2 || depts[0].LeaderOpenIDs[0] != "ou_leader" || depts[0].LeaderOpenIDs[1] != "ou_deputy" {
		t.Fatalf("dept[0] leaders = %v, want de-duplicated [ou_leader ou_deputy]", depts[0].LeaderOpenIDs)
	}
	if depts[1].ID != "od-2" || depts[1].ParentID != "od-1" {
		t.Fatalf("dept[1] = %+v", depts[1])
	}
}

func TestListUsersByDepartment(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/users/find_by_department",
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			dept := q.Get("department_id")
			c.inc("dept:" + dept)
			if q.Get("department_id_type") != "open_department_id" {
				t.Errorf("department_id_type = %q", q.Get("department_id_type"))
			}
			alice := map[string]any{
				"open_id": "ou_a", "union_id": "on_a", "user_id": "u_a", "name": "Alice", "email": "a@x.com",
				"avatar": map[string]any{"avatar_72": "http://img/a72.png", "avatar_240": "http://img/a240.png"},
				"status": map[string]any{"is_activated": true}, "department_ids": []string{"od-2", "od-3"},
			}
			var items []map[string]any
			token, more := "", false
			switch {
			case dept == "0":
				items = []map[string]any{{"open_id": "ou_ceo", "name": "CEO", "department_ids": []string{"0"}}}
			case dept == "od-2" && q.Get("page_token") == "":
				items, token, more = []map[string]any{alice}, "P2", true
			case dept == "od-2":
				items = []map[string]any{{"open_id": "ou_b", "name": "Bob", "status": map[string]any{"is_frozen": true}}}
			case dept == "od-3":
				items = []map[string]any{alice} // Alice is in two departments
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"items": items, "page_token": token, "has_more": more}})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	users, err := cl.ListUsers(context.Background(), []string{"od-2", "od-3"})
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3 (root member + Alice once + Bob): %+v", len(users), users)
	}
	if c.get("dept:0") != 1 || c.get("dept:od-2") != 2 || c.get("dept:od-3") != 1 {
		t.Fatalf("calls: root=%d od-2=%d od-3=%d", c.get("dept:0"), c.get("dept:od-2"), c.get("dept:od-3"))
	}
	byID := map[string]feishu.User{}
	for _, u := range users {
		byID[u.OpenID] = u
	}
	if a := byID["ou_a"]; !a.Active || a.AvatarURL != "http://img/a72.png" || len(a.DeptFeishuIDs) != 2 {
		t.Fatalf("alice = %+v", a)
	}
	if !byID["ou_ceo"].Active {
		t.Fatal("a user without status fields must count as active")
	}
	if byID["ou_b"].Active {
		t.Fatal("a frozen Feishu account must be inactive")
	}
}

// noDeptAuthority mimics Feishu rejecting a department outside the scope.
func noDeptAuthority(t *testing.T, w http.ResponseWriter) {
	w.WriteHeader(http.StatusForbidden)
	writeJSON(t, w, map[string]any{"code": 40004, "msg": "no dept authority error"})
}

func TestDirectoryPartialScope(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/departments/0/children",
		func(w http.ResponseWriter, r *http.Request) { noDeptAuthority(t, w) })
	mux.HandleFunc("/open-apis/contact/v3/scopes",
		func(w http.ResponseWriter, r *http.Request) {
			// Two pages: a granted department, then individual members
			// (ou_m is also a member of the department).
			if r.URL.Query().Get("page_token") == "" {
				writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
					"department_ids": []string{"od-team"}, "user_ids": []string{"ou_m"}, "has_more": true, "page_token": "P2"}})
				return
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"user_ids": []string{"ou_solo"}, "has_more": false}})
		})
	mux.HandleFunc("/open-apis/contact/v3/departments/od-team",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"department": map[string]any{
				"open_department_id": "od-team", "parent_department_id": "od-outside", "name": "Team",
				"leaders": []map[string]any{{"leader_type": 1, "leader_id": "ou_m"}}}}})
		})
	mux.HandleFunc("/open-apis/contact/v3/departments/od-team/children",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"items": []map[string]any{
				{"open_department_id": "od-sub", "parent_department_id": "od-team", "name": "Sub"}}}})
		})
	mux.HandleFunc("/open-apis/contact/v3/users/find_by_department",
		func(w http.ResponseWriter, r *http.Request) {
			dept := r.URL.Query().Get("department_id")
			c.inc("dept:" + dept)
			var items []map[string]any
			switch dept {
			case "od-team":
				items = []map[string]any{{"open_id": "ou_m", "name": "Member", "department_ids": []string{"od-team"}}}
			case "od-sub":
				items = []map[string]any{{"open_id": "ou_s", "name": "Sub member", "department_ids": []string{"od-sub"}}}
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"items": items}})
		})
	var batched []string
	mux.HandleFunc("/open-apis/contact/v3/users/batch",
		func(w http.ResponseWriter, r *http.Request) {
			batched = append(batched, r.URL.Query()["user_ids"]...)
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"items": []map[string]any{
				{"open_id": "ou_solo", "name": "Solo", "department_ids": []string{"od-elsewhere"},
					"status": map[string]any{"is_activated": true}}}}})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	depts, users, err := cl.Directory(context.Background())
	if err != nil {
		t.Fatalf("directory: %v", err)
	}
	if len(depts) != 2 || depts[0].ID != "od-team" || depts[0].Name != "Team" || depts[1].ID != "od-sub" {
		t.Fatalf("depts = %+v, want the granted department then its sub-department", depts)
	}
	if len(depts[0].LeaderOpenIDs) != 1 || depts[0].LeaderOpenIDs[0] != "ou_m" {
		t.Fatalf("leaders = %v", depts[0].LeaderOpenIDs)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.OpenID)
	}
	if len(users) != 3 || names[0] != "ou_m" || names[1] != "ou_s" || names[2] != "ou_solo" || !users[2].Active {
		t.Fatalf("users = %v, want department members then the individually granted one", names)
	}
	if len(batched) != 1 || batched[0] != "ou_solo" {
		t.Fatalf("batch fetched %v, want only the member not already listed", batched)
	}
	if c.get("dept:0") != 0 {
		t.Fatal("the root must not be listed when the scope is partial")
	}
}

func TestDirectoryOtherErrorsAreNotMaskedAsPartialScope(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/departments/0/children",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"code": 99991672, "msg": "access denied"})
		})
	mux.HandleFunc("/open-apis/contact/v3/scopes",
		func(w http.ResponseWriter, r *http.Request) { c.inc("scopes") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())
	if _, _, err := cl.Directory(context.Background()); err == nil || !contains(err.Error(), "99991672") {
		t.Fatalf("err = %v, want the original error", err)
	}
	if c.get("scopes") != 0 {
		t.Fatal("only 'no dept authority' should fall back to the scope API")
	}
}

func TestAPIErrorNonZeroCode(t *testing.T) {
	c := newCounter()
	mux := http.NewServeMux()
	tokenHandler(t, mux, c)
	mux.HandleFunc("/open-apis/contact/v3/departments/0/children",
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
	got := cl.OAuthURL("st ate", "https://cb/x?y=1", "")
	// Authorize lives on accounts.feishu.cn, uses client_id + response_type=code.
	if !hasPrefix(got, "https://accounts.feishu.cn/open-apis/authen/v1/authorize?") {
		t.Fatalf("url prefix wrong: %q", got)
	}
	if !contains(got, "client_id=cli_app") ||
		!contains(got, "response_type=code") ||
		!contains(got, "state=st+ate") ||
		!contains(got, "redirect_uri=https%3A%2F%2Fcb%2Fx%3Fy%3D1") {
		t.Fatalf("url missing encoded params: %q", got)
	}
	// Scope is included only when non-empty.
	if contains(got, "scope=") {
		t.Fatalf("empty scope should be omitted: %q", got)
	}
	got2 := cl.OAuthURL("s", "https://cb", "contact:user.base:readonly")
	if !contains(got2, "scope=contact") {
		t.Fatalf("scope not included: %q", got2)
	}
}
