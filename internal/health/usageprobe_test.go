package health_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
			{"name": "busy.json", "auth_index": "idx-busy", "provider": "codex",
				"id_token": map[string]any{"plan_type": "plus", "chatgpt_subscription_active_until": "2026-11-04T11:20:22+00:00"}},
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
		switch c.URL {
		case health.CodexResetCreditsURL:
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{"available_count":2,"credits":[
				{"id":"late","status":"available","title":"Full reset","expires_at":"2026-11-29T18:21:14.580904Z"},
				{"id":"soon","status":"available","title":"Full reset","expires_at":"2026-10-29T18:21:14.580904Z"},
				{"id":"used","status":"redeemed","title":"Full reset","expires_at":"2026-10-01T00:00:00Z"}]}`})
			return
		case health.CodexAccountsCheckURL:
			if c.AuthIndex == "idx-busy" { // this account's check fails
				_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 403, "body": `{"detail":"nope"}`})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": accountsCheckBody})
			return
		case health.CodexResetCreditConsumeURL:
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{"code":"reset","windows_reset":2}`})
			return
		}
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

	// A fresh snapshot whose window has since reset is probed again; one taken
	// after its (past) reset time is not.
	past, before := time.Now().Add(-time.Minute), time.Now().Add(-2*time.Minute)
	if err := st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: busy.ID, At: before, PrimaryUsedPct: &pct, PrimaryResetAt: &past}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	if err := p.RunStale(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	n := len(f.calls)
	f.mu.Unlock()
	if n != 0 {
		// busy's newest snapshot is still the fresh one without a reset time.
		t.Fatalf("probed %d accounts, want 0", n)
	}
	later := time.Now().Add(-30 * time.Second)
	if err := st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: busy.ID, At: later.Add(-time.Minute), PrimaryUsedPct: &pct, PrimaryResetAt: &later}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.Pool.Exec(ctx, `DELETE FROM quota_snapshots WHERE account_id=$1 AND primary_reset_at IS NULL`, busy.ID)
	if err := p.RunStale(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	var probedBusy bool
	for _, c := range f.calls {
		probedBusy = probedBusy || c.AuthIndex == "idx-busy"
	}
	f.mu.Unlock()
	if !probedBusy {
		t.Fatal("a snapshot taken before its window reset must be re-probed")
	}

	// A manual refresh right after a probe is rate limited per prober.
	if _, err := p.Refresh(ctx, idle.ID); !errors.Is(err, health.ErrRefreshTooSoon) {
		t.Fatalf("refresh err = %v, want ErrRefreshTooSoon", err)
	}
	fresh := &health.QuotaProber{Store: st, CPA: p.CPA, Settings: p.Settings}
	if _, err := fresh.Refresh(ctx, busy.ID); err != nil {
		t.Fatalf("refresh with no recent probe: %v", err)
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

func TestResetCredits(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)
	acc := mkAccount(t, st, ctx, box, "idle.json")
	until := time.Now().Add(3 * time.Hour)
	if err := st.SetRateLimitedUntil(ctx, acc.ID, &until); err != nil {
		t.Fatal(err)
	}
	acc, _ = st.GetAccount(ctx, acc.ID)
	f := &fakeCPAQuota{}
	srv := f.server(t)
	p := &health.QuotaProber{Store: st, CPA: cpa.NewClient(srv.URL, "mk", srv.Client()), Settings: settings.NewService(st)}

	c, err := p.ResetCredits(ctx, acc)
	if err != nil || c.Available != 2 || len(c.Credits) != 2 {
		t.Fatalf("credits %+v %v (want the 2 available ones)", c, err)
	}
	code, err := p.UseResetCredit(ctx, acc)
	if err != nil || code != health.ResetDone {
		t.Fatalf("use: %q %v", code, err)
	}
	f.mu.Lock()
	var consume cpa.APICallRequest
	usageAfter := false
	for _, call := range f.calls {
		if call.URL == health.CodexResetCreditConsumeURL {
			consume = call
		} else if consume.URL != "" && call.URL == health.CodexUsageURL {
			usageAfter = true
		}
	}
	f.mu.Unlock()
	if consume.Method != "POST" || !strings.Contains(consume.Data, `"credit_id":"soon"`) || !strings.Contains(consume.Data, `"redeem_request_id"`) ||
		consume.AuthIndex != "idx-idle" || consume.Header["ChatGPT-Account-Id"] != "acct-uuid-1" {
		t.Fatalf("consume call: %+v (want the credit expiring first, as the account)", consume)
	}
	if !usageAfter {
		t.Fatal("quota not refreshed after the reset")
	}
	after, _ := st.GetAccount(ctx, acc.ID)
	if after.RateLimitedUntil != nil {
		t.Fatalf("account still rate limited until %v", after.RateLimitedUntil)
	}
}

// accountsCheckBody is ChatGPT's accounts/check reply in the shape seen in
// production (2026-10-08): the account's entry plus a second one.
const accountsCheckBody = `{"accounts":{
	"default":{"account":{"plan_type":"free"},"entitlement":{"has_active_subscription":false,"subscription_plan":"chatgptfreeplan","expires_at":null,"renews_at":null,"cancels_at":null,"is_delinquent":false}},
	"acct-uuid-1":{"account":{"plan_type":"plus"},"entitlement":{"has_active_subscription":true,"is_active_subscription_gratis":false,
		"subscription_plan":"chatgptplusplan","expires_at":"2026-10-29T08:53:11+00:00","renews_at":"2026-10-29T02:53:11+00:00",
		"cancels_at":null,"billing_period":null,"is_delinquent":false,"billing_currency":"USD"}}},"account_ordering":["acct-uuid-1"]}`

func TestCheckSubscription(t *testing.T) {
	health.SetSubscriptionGap(0)
	st, ctx := newStore(t)
	box := newBox(t)
	idle := mkAccount(t, st, ctx, box, "idle.json")
	busy := mkAccount(t, st, ctx, box, "busy.json")
	f := &fakeCPAQuota{}
	srv := f.server(t)
	p := &health.QuotaProber{Store: st, CPA: cpa.NewClient(srv.URL, "mk", srv.Client()), Settings: settings.NewService(st)}

	sub, err := p.CheckSubscription(ctx, idle)
	if err != nil || sub.Plan != "plus" || !sub.Active || sub.RenewsAt == nil || sub.CancelsAt != nil ||
		sub.RenewsAt.UTC().Format(time.RFC3339) != "2026-10-29T02:53:11Z" {
		t.Fatalf("subscription %+v %v (want the account's own entry)", sub, err)
	}
	// A failed check keeps the id_token's paid-until date and says why.
	if _, err := p.CheckSubscription(ctx, busy); err == nil {
		t.Fatal("want the check to fail")
	}
	subs, _ := st.Subscriptions(ctx)
	if s := subs[busy.ID]; s.Error == "" || s.ExpiresAt == nil || s.ExpiresAt.UTC().Format(time.RFC3339) != "2026-11-04T11:20:22Z" || s.Plan != "plus" {
		t.Fatalf("fallback %+v", s)
	}
	if s := subs[idle.ID]; s.RenewsAt == nil || s.Error != "" {
		t.Fatalf("saved %+v", s)
	}

	// The background job checks only accounts not checked lately.
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	if err := p.RunSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	n := 0
	for _, c := range f.calls {
		if c.URL == health.CodexAccountsCheckURL {
			n++
		}
	}
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("checked %d fresh subscriptions again", n)
	}

	// A failed check is retried after half an hour, not six.
	if _, err := st.Pool.Exec(ctx, `UPDATE account_subscriptions SET checked_at = now() - interval '31 minutes'`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	_ = p.RunSubscriptions(ctx)
	f.mu.Lock()
	var checked []string
	for _, c := range f.calls {
		if c.URL == health.CodexAccountsCheckURL {
			checked = append(checked, c.AuthIndex)
		}
	}
	f.mu.Unlock()
	if len(checked) != 1 || checked[0] != "idx-busy" {
		t.Fatalf("retried %v, want only the failed one", checked)
	}
}
