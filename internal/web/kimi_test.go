package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"aiseat/internal/cpa"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

// fakeCPAManagement mimics the CPA management endpoints the Kimi flow uses.
func fakeCPAManagement(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	authorized := &atomic.Bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		files := []map[string]any{{"name": "codex-old.json", "type": "codex"}}
		if authorized.Load() {
			files = append(files, map[string]any{"name": "kimi-123.json", "type": "kimi"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	})
	mux.HandleFunc("/v0/management/kimi-auth-url", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "url": "https://auth.kimi.com/device?code=ABCD",
			"state": "st-1", "flow": "device", "user_code": "ABCD-1234"})
	})
	mux.HandleFunc("/v0/management/get-auth-status", func(w http.ResponseWriter, r *http.Request) {
		st := "wait"
		if authorized.Load() {
			st = "ok"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": st})
	})
	mux.HandleFunc("/v0/management/auth-files/fields", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, authorized
}

func TestKimiAccountOnboarding(t *testing.T) {
	cpaSrv, authorized := fakeCPAManagement(t)
	pool := testutil.NewDB(t)
	st := store.New(pool)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{
		Store: st, Settings: settings.NewService(st), Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
		CPA: cpa.NewClient(cpaSrv.URL, "mgmt", cpaSrv.Client()),
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	ctx := t.Context()

	_ = st.EnsureSuperAdmin(ctx, "ou_kadmin", "admin")
	admin := login(t, ts, "ou_kadmin")
	g, err := st.CreateIPGroup(ctx, box, "us-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}

	resp, body := admin.do(http.MethodPost, "/api/admin/accounts/kimi-start", `{"ip_group_id":`+itoa(g.ID)+`}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "ABCD-1234") {
		t.Fatalf("kimi-start: %d %s", resp.StatusCode, body)
	}

	authorized.Store(true) // the admin approved in the browser
	resp, body = admin.do(http.MethodPost, "/api/admin/accounts/kimi-complete", `{"state":"st-1"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("kimi-complete: %d %s", resp.StatusCode, body)
	}
	accs, _ := st.ListAccounts(ctx)
	if len(accs) != 1 || accs[0].Provider != store.ProviderKimi || accs[0].CPAAuthName != "kimi-123.json" {
		t.Fatalf("accounts: %+v", accs)
	}
	if accs[0].IsChatGPT() {
		t.Fatal("a Kimi account must not count as ChatGPT")
	}

	// Its employee's Codex snippet carries the key in the file (no environment
	// variable) and does not hard-code a ChatGPT model.
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_kuser", Name: "u"})
	_ = st.BindAccount(ctx, accs[0].ID, emp.ID, 3)
	_, body = login(t, ts, "ou_kuser").do(http.MethodGet, "/api/me/config-snippets", "")
	var snip struct {
		CodexToml string `json:"codex_toml"`
	}
	_ = json.Unmarshal(body, &snip)
	if !strings.Contains(snip.CodexToml, `experimental_bearer_token = "{{KEY}}"`) || strings.Contains(snip.CodexToml, "env_key") ||
		strings.Contains(snip.CodexToml, "gpt-6-luna") ||
		strings.Contains(snip.CodexToml, "x-openai-actor-authorization") || strings.Contains(snip.CodexToml, "supports_standalone_web_search") ||
		!strings.Contains(snip.CodexToml, "supports_websockets = false") {
		t.Fatalf("snippet for a Kimi account:\n%s", snip.CodexToml)
	}

	// A ChatGPT account's snippet uses the WebSocket transport like the official client.
	_, _ = st.Pool.Exec(ctx, `UPDATE upstream_accounts SET provider='codex'`)
	_, body = login(t, ts, "ou_kuser").do(http.MethodGet, "/api/me/config-snippets", "")
	_ = json.Unmarshal(body, &snip)
	// With no model list it recommends the configured default model.
	if !strings.Contains(snip.CodexToml, "supports_websockets = true") || !strings.Contains(snip.CodexToml, `model = "{{MODEL}}"`) ||
		!strings.Contains(snip.CodexToml, `"x-openai-actor-authorization"`) ||
		!strings.Contains(snip.CodexToml, "supports_standalone_web_search = true") {
		t.Fatalf("snippet for a ChatGPT account:\n%s", snip.CodexToml)
	}
}
