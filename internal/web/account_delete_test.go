package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// Removing an account frees its place in the IP group and deletes its CPA
// credential; usage logs stay (requests.account_id has no foreign key).
func TestDeleteAccountLeavesItsIPGroup(t *testing.T) {
	var mu sync.Mutex
	deleted := ""
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v0/management/auth-files" {
			mu.Lock()
			deleted = r.URL.Query().Get("name")
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{}`))
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
	g, _ := st.CreateIPGroup(ctx, box, "direct", "direct", 4)
	acc, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "codex-x.json", Email: "x@example.com", IPGroupID: g.ID})
	_ = st.BindAccount(ctx, acc.ID, e.ID, 3)
	_ = st.CreateOnboarding(ctx, store.Onboarding{State: "s1", IPGroupID: g.ID, KnownAuthNames: []string{}})
	_ = st.CompleteOnboarding(ctx, "s1", acc.ID)
	if _, err := pool.Exec(ctx, `INSERT INTO requests (id, started_at, employee_id, account_id, client_kind, endpoint, status_code)
		VALUES (gen_random_uuid(), now(), $1, $2, 'codex', 'responses', 200)`, e.ID, acc.ID); err != nil {
		t.Fatal(err)
	}

	plain := login(t, ts, "ou_plain")
	if resp, _ := plain.do(http.MethodDelete, "/api/admin/accounts/"+itoa(acc.ID), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a non-admin removed an account: %d", resp.StatusCode)
	}
	if resp, body := admin.do(http.MethodDelete, "/api/admin/accounts/"+itoa(acc.ID), ""); resp.StatusCode != 200 {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	if _, err := st.GetAccount(ctx, acc.ID); err != store.ErrNotFound {
		t.Fatalf("account still there: %v", err)
	}
	if accs, _ := st.AccountsOf(ctx, e.ID); len(accs) != 0 {
		t.Fatalf("still a member: %+v", accs)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM requests WHERE employee_id=$1`, e.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("usage log lost: %d", n)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM upstream_accounts WHERE ip_group_id=$1`, g.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("IP group still holds %d accounts", n)
	}
	mu.Lock()
	if deleted != "codex-x.json" {
		t.Fatalf("CPA credential deleted: %q", deleted)
	}
	mu.Unlock()
	if resp, _ := admin.do(http.MethodDelete, "/api/admin/accounts/"+itoa(acc.ID), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: %d", resp.StatusCode)
	}
}
