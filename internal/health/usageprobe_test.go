package health_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/health"
	"aiseat/internal/settings"
	"aiseat/internal/store"
)

func TestParseCodexUsage(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	body := `{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,
		"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1790003600},
		"secondary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_after_seconds":172800,"reset_at":1790172800}},
		"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}`
	q, plan, err := health.ParseCodexUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	if plan != "pro" {
		t.Fatalf("plan = %q", plan)
	}
	if *q.PrimaryUsedPct != 12 || *q.PrimaryWindowMinutes != 300 || !q.PrimaryResetAt.Equal(time.Unix(1790003600, 0)) {
		t.Fatalf("primary = %v %v %v", *q.PrimaryUsedPct, *q.PrimaryWindowMinutes, q.PrimaryResetAt)
	}
	if *q.SecondaryUsedPct != 40 || *q.SecondaryWindowMinutes != 10080 {
		t.Fatalf("secondary = %v %v", *q.SecondaryUsedPct, *q.SecondaryWindowMinutes)
	}

	// Windows reported the other way round are normalised: primary = shorter.
	swapped := `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_after_seconds":60},
		"secondary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":60}}}`
	q, _, err = health.ParseCodexUsage([]byte(swapped), now)
	if err != nil || *q.PrimaryWindowMinutes != 300 || *q.PrimaryUsedPct != 12 || !q.PrimaryResetAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("swapped: %+v %v", q, err)
	}

	// A Pro plan reporting only its weekly window, as "primary".
	proWeekly := `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":9,"limit_window_seconds":604800,"reset_at":1790172800},"secondary_window":null}}`
	q, _, err = health.ParseCodexUsage([]byte(proWeekly), now)
	if err != nil || q.PrimaryUsedPct != nil || *q.SecondaryUsedPct != 9 || *q.SecondaryWindowMinutes != 10080 {
		t.Fatalf("pro weekly-only: %+v %v", q, err)
	}

	// A plan without limits (e.g. a free or API-key login) is an error, not zeros.
	if _, _, err := health.ParseCodexUsage([]byte(`{"plan_type":"free","rate_limit":null}`), now); err == nil {
		t.Fatal("want error when there are no windows")
	}
}

// fakeCPAQuota serves the two management endpoints the prober uses.
type fakeCPAQuota struct {
	mu    sync.Mutex
	calls []cpa.APICallRequest
}

func (f *fakeCPAQuota) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{
			{"name": "busy.json", "auth_index": "idx-busy", "provider": "codex"},
			{"name": "idle.json", "auth_index": "idx-idle", "provider": "codex",
				"id_token": map[string]any{"chatgpt_account_id": "acct-uuid-1", "plan_type": "plus"}},
			{"name": "kimi.json", "auth_index": "idx-kimi", "provider": "kimi"},
		}})
	})
	mux.HandleFunc("/v0/management/api-call", func(w http.ResponseWriter, r *http.Request) {
		var c cpa.APICallRequest
		_ = json.NewDecoder(r.Body).Decode(&c)
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		if c.URL == health.KimiUsageURL {
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": kimiUsageBody})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{"plan_type":"pro","rate_limit":{
			"primary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_at":1790003600},
			"secondary_window":{"used_percent":33,"limit_window_seconds":604800,"reset_at":1790172800}}}`})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestQuotaProberProbesOnlyStaleAccounts(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)
	busy := mkAccount(t, st, ctx, box, "busy.json")
	idle := mkAccount(t, st, ctx, box, "idle.json")
	kimi := mkAccount(t, st, ctx, box, "kimi.json")
	if _, err := st.Pool.Exec(ctx, `UPDATE upstream_accounts SET provider='kimi' WHERE id=$1`, kimi.ID); err != nil {
		t.Fatal(err)
	}
	// busy has a fresh snapshot from its own traffic; idle has none.
	pct := 50.0
	if err := st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: busy.ID, At: time.Now(), PrimaryUsedPct: &pct}); err != nil {
		t.Fatal(err)
	}

	f := &fakeCPAQuota{}
	srv := f.server(t)
	p := &health.QuotaProber{Store: st, CPA: cpa.NewClient(srv.URL, "mk", srv.Client()), Settings: settings.NewService(st)}
	if err := p.RunStale(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	calls := append([]cpa.APICallRequest(nil), f.calls...)
	f.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("probed %d accounts, want 2 (the idle ChatGPT one and the Kimi one)", len(calls))
	}
	byIdx := map[string]cpa.APICallRequest{}
	for _, c := range calls {
		byIdx[c.AuthIndex] = c
	}
	if k := byIdx["idx-kimi"]; k.URL != health.KimiUsageURL || k.Header["Authorization"] != "Bearer $TOKEN$" || len(k.Header) != 1 {
		t.Fatalf("kimi api-call = %+v", k)
	}
	if q, err := st.LatestQuota(ctx, kimi.ID); err != nil || q.SecondaryUsedPct == nil || *q.PrimaryWindowMinutes != 300 {
		t.Fatalf("kimi snapshot = %+v, %v", q, err)
	}
	c := byIdx["idx-idle"]
	if c.AuthIndex != "idx-idle" || c.Method != "GET" || c.URL != health.CodexUsageURL ||
		c.Header["Authorization"] != "Bearer $TOKEN$" || c.Header["ChatGPT-Account-Id"] != "acct-uuid-1" || c.Header["User-Agent"] == "" {
		t.Fatalf("api-call = %+v", c)
	}
	q, err := st.LatestQuota(ctx, idle.ID)
	if err != nil || *q.PrimaryUsedPct != 7 || *q.SecondaryUsedPct != 33 {
		t.Fatalf("snapshot = %+v, %v", q, err)
	}
	if a, _ := st.GetAccount(ctx, idle.ID); a.PlanType != "pro" {
		t.Fatalf("plan type not updated: %q", a.PlanType)
	}

	// A manual refresh right after is rate limited; another account is not.
	if _, err := p.Refresh(ctx, idle.ID); !errors.Is(err, health.ErrRefreshTooSoon) {
		t.Fatalf("refresh err = %v, want ErrRefreshTooSoon", err)
	}
	if _, err := p.Refresh(ctx, busy.ID); err != nil {
		t.Fatalf("refresh busy: %v", err)
	}
}

func TestNormalizeQuotaWindows(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	i := func(v int) *int { return &v }
	// Pro plans can report only a weekly window, in the primary position.
	q := health.NormalizeQuotaWindows(store.QuotaSnapshot{PrimaryUsedPct: f(30), PrimaryWindowMinutes: i(10080)})
	if q.PrimaryUsedPct != nil || q.SecondaryUsedPct == nil || *q.SecondaryUsedPct != 30 {
		t.Fatalf("weekly-only: %+v", q)
	}
	// A lone 5-hour window reported as secondary moves to primary.
	q = health.NormalizeQuotaWindows(store.QuotaSnapshot{SecondaryUsedPct: f(5), SecondaryWindowMinutes: i(300)})
	if q.PrimaryUsedPct == nil || *q.PrimaryUsedPct != 5 || q.SecondaryUsedPct != nil {
		t.Fatalf("5h-only: %+v", q)
	}
	// Unknown lengths keep the upstream order.
	q = health.NormalizeQuotaWindows(store.QuotaSnapshot{PrimaryUsedPct: f(1), SecondaryUsedPct: f(2)})
	if *q.PrimaryUsedPct != 1 || *q.SecondaryUsedPct != 2 {
		t.Fatalf("unknown: %+v", q)
	}
}

// The body below is a real Kimi Code /usages response with IDs removed.
const kimiUsageBody = `{"usage":{"limit":"100","used":"3","remaining":"97","resetTime":"2026-10-02T02:27:06Z"},
	"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","remaining":"100","resetTime":"2026-09-28T10:27:06Z"}}],
	"booster_wallet":{"status":"STATUS_DISABLED"},
	"usages":{"limit_5h":{"used_ratio":0,"reset_time":"2026-09-28T10:27:05Z"},"limit_7d":{"used_ratio":0.027983,"reset_time":"2026-10-02T02:27:04Z"}}}`

func TestParseKimiUsage(t *testing.T) {
	now := time.Now()
	q, err := health.ParseKimiUsage([]byte(kimiUsageBody), now)
	if err != nil {
		t.Fatal(err)
	}
	if *q.PrimaryUsedPct != 0 || *q.PrimaryWindowMinutes != 300 || q.PrimaryResetAt.UTC().Format(time.RFC3339) != "2026-09-28T10:27:05Z" {
		t.Fatalf("5h = %v %v %v", *q.PrimaryUsedPct, *q.PrimaryWindowMinutes, q.PrimaryResetAt)
	}
	if got := *q.SecondaryUsedPct; got < 2.79 || got > 2.81 || *q.SecondaryWindowMinutes != 10080 {
		t.Fatalf("week = %v %v", got, *q.SecondaryWindowMinutes)
	}

	// Without the summary it falls back to the fields kimi-cli displays.
	fallback := `{"usage":{"limit":"100","used":"40","resetTime":"2026-10-02T02:27:06Z"},
		"limits":[{"window":{"duration":5,"timeUnit":"TIME_UNIT_HOUR"},"detail":{"limit":"200","remaining":"150"}}]}`
	q, err = health.ParseKimiUsage([]byte(fallback), now)
	if err != nil || *q.PrimaryUsedPct != 25 || *q.PrimaryWindowMinutes != 300 || *q.SecondaryUsedPct != 40 {
		t.Fatalf("fallback: %+v %v", q, err)
	}
	if _, err := health.ParseKimiUsage([]byte(`{}`), now); err == nil {
		t.Fatal("want error for a body without limits")
	}
}
