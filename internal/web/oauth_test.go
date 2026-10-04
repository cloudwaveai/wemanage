package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiseat/internal/cpa"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

// A pasted callback CPA refuses comes back as a plain explanation, and 410
// when the login is over and needs a new link.
func TestOAuthCompleteExplainsRefusals(t *testing.T) {
	var cpaAnswer struct {
		status int
		body   string
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v0/management/codex-auth-url":
			_, _ = io.WriteString(w, `{"status":"ok","url":"https://auth.openai.com/x","state":"st-now"}`)
		case "/v0/management/auth-files":
			_, _ = io.WriteString(w, `{"files":[]}`)
		case "/v0/management/oauth-callback":
			w.WriteHeader(cpaAnswer.status)
			_, _ = io.WriteString(w, cpaAnswer.body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)

	pool := testutil.NewDB(t)
	st := store.New(pool)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{
		Store: st, Settings: settings.NewService(st), Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
		CPA: cpa.NewClient(fake.URL, "k", fake.Client()),
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	ctx := context.Background()

	admin := login(t, ts, "ou_admin")
	e, _ := st.GetEmployeeByOpenID(ctx, "ou_admin")
	_ = st.SetSuperAdmin(ctx, e.ID, true)
	g, err := st.CreateIPGroup(ctx, box, "g", "direct", 10)
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := admin.do(http.MethodPost, "/api/admin/accounts/oauth-start", `{"ip_group_id":`+itoa(g.ID)+`}`); resp.StatusCode != 200 {
		t.Fatalf("start: %d %s", resp.StatusCode, body)
	}

	complete := func(redirect string) (int, string) {
		resp, body := admin.do(http.MethodPost, "/api/admin/accounts/oauth-complete",
			`{"state":"st-now","redirect_url":"`+redirect+`"}`)
		return resp.StatusCode, string(body)
	}

	// A callback from an earlier link never reaches CPA.
	if code, body := complete("http://localhost:1455/auth/callback?code=c&state=st-old"); code != http.StatusBadRequest || !strings.Contains(body, "之前的另一个登录链接") {
		t.Fatalf("earlier link: %d %s", code, body)
	}
	cpaAnswer.status, cpaAnswer.body = http.StatusConflict, `{"error":"Timeout waiting for OAuth callback","status":"error"}`
	if code, body := complete("http://localhost:1455/auth/callback?code=c&state=st-now"); code != http.StatusGone || !strings.Contains(body, "5 分钟") {
		t.Fatalf("timed out: %d %s", code, body)
	}
	cpaAnswer.status, cpaAnswer.body = http.StatusBadRequest, `{"error":"code or error is required","status":"error"}`
	if code, body := complete("http://localhost:1455/auth/callback?state=st-now"); code != http.StatusBadRequest || !strings.Contains(body, "localhost:1455") {
		t.Fatalf("no code: %d %s", code, body)
	}
}
