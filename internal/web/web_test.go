package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store, context.Context) {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{
		Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, st, context.Background()
}

// client keeps cookies and the CSRF token across calls.
type client struct {
	t    *testing.T
	base string
	jar  []*http.Cookie
	csrf string
	hc   *http.Client
}

func login(t *testing.T, ts *httptest.Server, openID string) *client {
	c := &client{t: t, base: ts.URL, hc: ts.Client()}
	body := `{"open_id":"` + openID + `","name":"` + openID + `"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/dev", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("dev login status %d", resp.StatusCode)
	}
	c.jar = resp.Cookies()
	var out struct{ CSRF string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	c.csrf = out.CSRF
	return c
}

func (c *client) do(method, path, body string) (*http.Response, []byte) {
	c.t.Helper()
	var r *http.Request
	if body != "" {
		r, _ = http.NewRequest(method, c.base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r, _ = http.NewRequest(method, c.base+path, nil)
	}
	for _, ck := range c.jar {
		r.AddCookie(ck)
	}
	if method != http.MethodGet {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.hc.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0)
	tmp := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
	}
	return resp, buf
}

func TestUnauthenticated(t *testing.T) {
	ts, _, _ := newServer(t)
	resp, _ := (&client{t: t, base: ts.URL, hc: ts.Client()}).do(http.MethodGet, "/api/me", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCSRFRequired(t *testing.T) {
	ts, _, _ := newServer(t)
	c := login(t, ts, "ou_csrf")
	// POST without CSRF header should be rejected.
	saved := c.csrf
	c.csrf = ""
	resp, _ := c.do(http.MethodPost, "/api/me/ack-notice", "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF)", resp.StatusCode)
	}
	c.csrf = saved
}

func TestNoticeAndKeyFlow(t *testing.T) {
	ts, _, _ := newServer(t)
	c := login(t, ts, "ou_key")

	// Issuing a key before acknowledging the notice is forbidden.
	resp, _ := c.do(http.MethodPost, "/api/me/key", "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("key before ack: status %d, want 403", resp.StatusCode)
	}
	// Acknowledge, then issue.
	resp, _ = c.do(http.MethodPost, "/api/me/ack-notice", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("ack status %d", resp.StatusCode)
	}
	resp, body := c.do(http.MethodPost, "/api/me/key", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("key status %d", resp.StatusCode)
	}
	var out struct{ Key string }
	_ = json.Unmarshal(body, &out)
	if !strings.HasPrefix(out.Key, "sk-emp-") {
		t.Fatalf("key = %q", out.Key)
	}
}

func TestAdminRequiresSuperAdmin(t *testing.T) {
	ts, _, _ := newServer(t)
	c := login(t, ts, "ou_plain")
	resp, _ := c.do(http.MethodGet, "/api/admin/overview", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLeaderScopeOnLogs(t *testing.T) {
	ts, st, ctx := newServer(t)

	// Build an org: leader + subordinate in dept D, plus an outsider.
	leader, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_leader", Name: "leader"})
	sub, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_sub", Name: "sub"})
	outsider, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_out", Name: "out"})
	dRow, _ := st.UpsertDepartment(ctx, store.Department{FeishuDeptID: "d1", Name: "D", LeaderOpenIDs: []string{"ou_leader"}})
	_ = st.SetEmployeeDepartments(ctx, leader.ID, []int64{dRow})
	_ = st.SetEmployeeDepartments(ctx, sub.ID, []int64{dRow})
	oRow, _ := st.UpsertDepartment(ctx, store.Department{FeishuDeptID: "d2", Name: "O"})
	_ = st.SetEmployeeDepartments(ctx, outsider.ID, []int64{oRow})

	c := login(t, ts, "ou_leader")

	// Leader may view the subordinate.
	resp, _ := c.do(http.MethodGet, "/api/logs?employee_id="+itoa(sub.ID), "")
	if resp.StatusCode != 200 {
		t.Fatalf("leader viewing subordinate: status %d, want 200", resp.StatusCode)
	}
	// Leader may NOT view the outsider.
	resp, _ = c.do(http.MethodGet, "/api/logs?employee_id="+itoa(outsider.ID), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leader viewing outsider: status %d, want 403", resp.StatusCode)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
