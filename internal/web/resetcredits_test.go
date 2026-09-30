package web_test

import (
	"encoding/json"
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

func TestResetCreditsNeedHighUsage(t *testing.T) {
	var consumed atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"name": "c.json", "auth_index": "idx", "provider": "codex"}}})
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
	_ = st.BindAccount(ctx, acc.ID, emp.ID)

	_, raw := c.do(http.MethodGet, "/api/me/reset-credits", "")
	var got struct {
		Available int  `json:"available"`
		CanUse    bool `json:"can_use"`
	}
	_ = json.Unmarshal(raw, &got)
	if got.Available != 1 || got.CanUse {
		t.Fatalf("low usage: %s (want 1 card, not usable yet)", raw)
	}
	if resp, body := c.do(http.MethodPost, "/api/me/reset-credits/use", ""); resp.StatusCode != http.StatusConflict || consumed.Load() != 0 {
		t.Fatalf("use at low usage: %d %s, consumed %d", resp.StatusCode, body, consumed.Load())
	}

	p := 85.0
	_ = st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: acc.ID, At: time.Now(), PrimaryUsedPct: &p})
	resp, body := c.do(http.MethodPost, "/api/me/reset-credits/use", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"code":"reset"`) || consumed.Load() != 1 {
		t.Fatalf("use at 85%%: %d %s", resp.StatusCode, body)
	}
}
