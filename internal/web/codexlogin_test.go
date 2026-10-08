package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"aiseat/internal/cpa"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

func fakeIDToken(email, accountID string) string {
	enc := base64.RawURLEncoding.EncodeToString
	claims, _ := json.Marshal(map[string]any{"email": email, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID}})
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(claims) + ".sig"
}

// The gateway logs ChatGPT accounts in like the official Codex CLI (127.0.0.1
// callback, PKCE) and gives CPA the credential; a second login of the same
// account refreshes it instead of adding another.
func TestCodexLoginByTheGateway(t *testing.T) {
	var mu sync.Mutex
	var tokenForm url.Values
	files := map[string]string{} // CPA auth files by name
	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		mu.Lock()
		tokenForm = r.PostForm
		mu.Unlock()
		if r.PostForm.Get("code") == "api-style" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"Could not validate your token.","type":"invalid_request_error","code":"token_expired"}}`)
			return
		}
		if r.PostForm.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"code expired"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token": fakeIDToken("plus3@example.com", "acc-123"), "access_token": "at", "refresh_token": "rt", "expires_in": 864000,
		})
	}))
	t.Cleanup(openai.Close)
	fakeCPA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/v0/management/auth-files" && r.Method == http.MethodPost:
			var b struct{ Name, Content string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			files[b.Name] = b.Content
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case r.URL.Path == "/v0/management/auth-files":
			list := []map[string]any{}
			for name, c := range files {
				var f struct {
					Type      string `json:"type"`
					Email     string `json:"email"`
					AccountID string `json:"account_id"`
				}
				_ = json.Unmarshal([]byte(c), &f)
				list = append(list, map[string]any{"name": name, "type": f.Type, "email": f.Email,
					"id_token": map[string]any{"chatgpt_account_id": f.AccountID}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": list})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fakeCPA.Close)

	pool := testutil.NewDB(t)
	st := store.New(pool)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{
		Store: st, Settings: settings.NewService(st), Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
		CPA: cpa.NewClient(fakeCPA.URL, "k", fakeCPA.Client()), CodexAuthBase: openai.URL,
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	admin := login(t, ts, "ou_admin")
	e, _ := st.GetEmployeeByOpenID(ctx, "ou_admin")
	_ = st.SetSuperAdmin(ctx, e.ID, true)
	g, _ := st.CreateIPGroup(ctx, box, "g", "direct", 10)

	start := func() (state string, link *url.URL) {
		resp, body := admin.do(http.MethodPost, "/api/admin/accounts/login-start", `{"provider":"codex","ip_group_id":`+itoa(g.ID)+`}`)
		var out struct {
			AuthURL  string `json:"auth_url"`
			State    string
			Callback string
		}
		_ = json.Unmarshal(body, &out)
		if resp.StatusCode != 200 || out.Callback != "http://127.0.0.1:1455/auth/callback" {
			t.Fatalf("start: %d %s", resp.StatusCode, body)
		}
		link, _ = url.Parse(out.AuthURL)
		return out.State, link
	}
	complete := func(state, cb string) (int, string) {
		resp, body := admin.do(http.MethodPost, "/api/admin/accounts/login-complete", `{"state":"`+state+`","redirect_url":"`+cb+`"}`)
		return resp.StatusCode, string(body)
	}

	state, link := start()
	q := link.Query()
	if link.Path != "/oauth/authorize" || q.Get("client_id") != "app_EMoamEEZ73f0CkXaXp7hrann" ||
		q.Get("redirect_uri") != "http://127.0.0.1:1455/auth/callback" || q.Get("originator") != "codex_cli_rs" ||
		!strings.Contains(q.Get("scope"), "offline_access") || q.Has("prompt") || q.Get("state") != state || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize link: %s", link)
	}

	// A bad code is explained; the login is then used up.
	if code, body := complete(state, "http://127.0.0.1:1455/auth/callback?code=bad&state="+state); code != http.StatusGone || !strings.Contains(body, "code expired") {
		t.Fatalf("bad code: %d %s", code, body)
	}

	state, _ = start()
	if code, body := complete(state, "http://127.0.0.1:1455/auth/callback?code=api-style&state="+state); code != http.StatusGone ||
		!strings.Contains(body, "token_expired Could not validate your token.") {
		t.Fatalf("api-style error: %d %s", code, body)
	}

	state, link = start()
	if code, body := complete(state, "http://127.0.0.1:1455/auth/callback?code=good-code&state="+state); code != 200 || !strings.Contains(body, "plus3@example.com") {
		t.Fatalf("complete: %d %s", code, body)
	}
	mu.Lock()
	sum := sha256.Sum256([]byte(tokenForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != link.Query().Get("code_challenge") ||
		tokenForm.Get("redirect_uri") != "http://127.0.0.1:1455/auth/callback" || tokenForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("token request: %v", tokenForm)
	}
	if len(files) != 1 {
		t.Fatalf("CPA files: %v", files)
	}
	for name, c := range files {
		var f map[string]any
		_ = json.Unmarshal([]byte(c), &f)
		if !strings.HasPrefix(name, "codex-plus3@example.com-") || f["type"] != "codex" || f["refresh_token"] != "rt" ||
			f["account_id"] != "acc-123" || f["email"] != "plus3@example.com" || f["expired"] == "" {
			t.Fatalf("CPA file %s: %s", name, c)
		}
	}
	mu.Unlock()
	accs, _ := st.ListAccounts(ctx)
	if len(accs) != 1 || accs[0].Email != "plus3@example.com" || accs[0].Provider != store.ProviderCodex {
		t.Fatalf("accounts: %+v", accs)
	}

	// The same callback again: the login is used up.
	if code, _ := complete(state, "http://127.0.0.1:1455/auth/callback?code=good-code&state="+state); code != http.StatusGone {
		t.Fatalf("reused login: %d", code)
	}
	// Logging the same account in again refreshes it.
	state, _ = start()
	if code, body := complete(state, "http://127.0.0.1:1455/auth/callback?code=good-code&state="+state); code != 200 || !strings.Contains(body, `"updated":true`) {
		t.Fatalf("relogin: %d %s", code, body)
	}
	mu.Lock()
	n := len(files)
	mu.Unlock()
	if accs, _ := st.ListAccounts(ctx); len(accs) != 1 || n != 1 {
		t.Fatalf("relogin added an account: %d accounts, %d files", len(accs), n)
	}
	// OpenAI's own error in the callback.
	state, _ = start()
	if code, body := complete(state, "http://127.0.0.1:1455/auth/callback?error=access_denied&state="+state); code != http.StatusGone || !strings.Contains(body, "access_denied") {
		t.Fatalf("denied: %d %s", code, body)
	}
}
