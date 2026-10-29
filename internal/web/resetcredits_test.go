package web_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/health"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

func TestResetCreditsUsableWhileQuotaRemains(t *testing.T) {
	var consumed atomic.Int32
	var failCleanup atomic.Bool
	failCleanup.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"name": "c.json", "auth_index": "idx", "provider": "codex"}}})
	})
	mux.HandleFunc("/v0/management/reset-quota", func(w http.ResponseWriter, r *http.Request) {
		if failCleanup.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v0/management/api-call", func(w http.ResponseWriter, r *http.Request) {
		var c cpa.APICallRequest
		_ = json.NewDecoder(r.Body).Decode(&c)
		body := `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1890000000}}}`
		switch c.URL {
		case health.CodexResetCreditsURL:
			body = `{"available_count":1,"credits":[{"id":"c1","status":"available","title":"Full reset (Weekly + 5 hr)","expires_at":"2026-10-29T18:21:14Z"}]}`
		case health.CodexResetCreditConsumeURL:
			consumed.Add(1)
			body = `{"code":"reset","windows_reset":2}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body})
	})
	fake := httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	st := store.New(testutil.NewDB(t))
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
		Quota: &health.QuotaProber{Store: st, CPA: cpa.NewClient(fake.URL, "mk", fake.Client()), Settings: set}})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	c := login(t, ts, "ou_rc")
	ctx := t.Context()
	emp, _ := st.GetEmployeeByOpenID(ctx, "ou_rc")
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	acc, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "c.json", IPGroupID: g.ID, Provider: "codex"})
	_ = st.BindAccount(ctx, acc.ID, emp.ID, 3)

	_, raw := c.do(http.MethodGet, "/api/me/reset-credits", "")
	var list struct {
		Accounts []struct {
			AccountID int64 `json:"account_id"`
			Available int   `json:"available"`
			CanUse    bool  `json:"can_use"`
		} `json:"accounts"`
	}
	_ = json.Unmarshal(raw, &list)
	if len(list.Accounts) != 1 || list.Accounts[0].AccountID != acc.ID || list.Accounts[0].Available != 1 || !list.Accounts[0].CanUse {
		t.Fatalf("one card: %s (want 1 card, usable)", raw)
	}
	resp, body := c.do(http.MethodPost, "/api/me/reset-credits/use", "")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"code":"reset_routing_pending"`) || !strings.Contains(string(body), "不要再次使用") || consumed.Load() != 1 {
		t.Fatalf("cleanup failure: %d %s", resp.StatusCode, body)
	}
	failCleanup.Store(false)
	resp, body = c.do(http.MethodPost, "/api/me/reset-credits/use", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"code":"reset"`) || consumed.Load() != 2 {
		t.Fatalf("use while quota remains: %d %s", resp.StatusCode, body)
	}
}

func TestAdminResetCreditAndSafeLimitRecovery(t *testing.T) {
	var allowed atomic.Bool
	var unknown atomic.Bool
	var cpaClears atomic.Int32
	var consumes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"name": "admin-c.json", "auth_index": "admin-idx", "provider": "codex"}}})
	})
	mux.HandleFunc("/v0/management/reset-quota", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			AuthIndex string `json:"auth_index"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.AuthIndex != "admin-idx" {
			http.Error(w, "wrong account", 400)
			return
		}
		cpaClears.Add(1)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v0/management/api-call", func(w http.ResponseWriter, r *http.Request) {
		var call cpa.APICallRequest
		_ = json.NewDecoder(r.Body).Decode(&call)
		body := `{"rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000}}}`
		if allowed.Load() {
			body = `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":10,"limit_window_seconds":18000}}}`
		}
		if unknown.Load() {
			body = `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}}`
		}
		switch call.URL {
		case health.CodexResetCreditsURL:
			body = `{"available_count":1,"credits":[{"id":"c1","status":"available","expires_at":"2026-12-01T00:00:00Z"}]}`
		case health.CodexResetCreditConsumeURL:
			consumes.Add(1)
			body = `{"code":"reset"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body})
	})
	fake := httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	st := store.New(testutil.NewDB(t))
	ctx := t.Context()
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	_ = st.EnsureSuperAdmin(ctx, "ou_credit_admin", "admin")
	srv := web.NewServer(web.Deps{Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true, Quota: &health.QuotaProber{Store: st, CPA: cpa.NewClient(fake.URL, "mk", fake.Client()), Settings: set}})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	admin := login(t, ts, "ou_credit_admin")
	user := login(t, ts, "ou_credit_user")
	group, _ := st.CreateIPGroup(ctx, box, "admin-credit-group", "", 10)
	acc, err := st.CreateAccount(ctx, store.Account{CPAAuthName: "admin-c.json", IPGroupID: group.ID, Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := st.SetRateLimitedUntil(ctx, acc.ID, &until); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/admin/accounts/%d", acc.ID)
	for _, call := range []struct{ method, path string }{{"GET", "/reset-credits"}, {"POST", "/reset-credits/use"}, {"POST", "/rate-limit/reconcile"}} {
		resp, _ := user.do(call.method, base+call.path, "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("non-admin %s %s: %d", call.method, call.path, resp.StatusCode)
		}
	}
	resp, body := admin.do("GET", base+"/reset-credits", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"available":1`) || !strings.Contains(string(body), `"id":"c1"`) {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	resp, body = admin.do("POST", base+"/rate-limit/reconcile", "")
	if resp.StatusCode != http.StatusConflict || cpaClears.Load() != 0 {
		t.Fatalf("limited upstream: %d %s", resp.StatusCode, body)
	}
	after, _ := st.GetAccount(ctx, acc.ID)
	if after.RateLimitedUntil == nil {
		t.Fatal("upstream limit was cleared")
	}
	unknown.Store(true)
	resp, body = admin.do("POST", base+"/rate-limit/reconcile", "")
	if resp.StatusCode != http.StatusConflict || cpaClears.Load() != 0 {
		t.Fatalf("unknown upstream: %d %s", resp.StatusCode, body)
	}
	unknown.Store(false)
	allowed.Store(true)
	resp, body = admin.do("POST", base+"/rate-limit/reconcile", "")
	if resp.StatusCode != 200 || cpaClears.Load() != 1 {
		t.Fatalf("reconcile %d %s", resp.StatusCode, body)
	}
	after, _ = st.GetAccount(ctx, acc.ID)
	if after.RateLimitedUntil != nil {
		t.Fatal("stale limit remains")
	}
	resp, body = admin.do("POST", base+"/reset-credits/use", "")
	if resp.StatusCode != 200 || consumes.Load() != 1 || cpaClears.Load() != 2 {
		t.Fatalf("admin use %d %s", resp.StatusCode, body)
	}
	var auditCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE target_id=$1 AND action IN ('reset_credit_use','rate_limit_reconcile') AND actor_employee_id IS NOT NULL`, fmt.Sprint(acc.ID)).Scan(&auditCount); err != nil || auditCount != 4 {
		t.Fatalf("audit count %d: %v", auditCount, err)
	}
}
