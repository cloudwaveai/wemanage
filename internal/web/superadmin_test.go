package web_test

import (
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

func TestSuperAdminToggle(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	box, _ := secure.NewBox(make([]byte, 32))
	srv := web.NewServer(web.Deps{
		Store: st, Settings: settings.NewService(st), Box: box, PublicBaseURL: "http://localhost",
		DevLogin: true, BootstrapSuperAdmins: []string{"ou_pinned"},
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	ctx := t.Context()

	_ = st.EnsureSuperAdmin(ctx, "ou_boss", "boss")
	_ = st.EnsureSuperAdmin(ctx, "ou_pinned", "pinned")
	boss := login(t, ts, "ou_boss")
	peer, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_peer", Name: "peer"})
	me, _ := st.GetEmployeeByOpenID(ctx, "ou_boss")
	pinned, _ := st.GetEmployeeByOpenID(ctx, "ou_pinned")
	path := func(id int64) string { return "/api/admin/employees/" + itoa(id) + "/super-admin" }

	// A plain employee cannot grant anything.
	if resp, _ := login(t, ts, "ou_peer").do(http.MethodPost, path(peer.ID), `{"value":true}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain employee: status %d, want 403", resp.StatusCode)
	}
	// Grant, then the new admin reaches admin endpoints right away.
	if resp, body := boss.do(http.MethodPost, path(peer.ID), `{"value":true}`); resp.StatusCode != 200 {
		t.Fatalf("grant: %d %s", resp.StatusCode, body)
	}
	if resp, _ := login(t, ts, "ou_peer").do(http.MethodGet, "/api/admin/employees", ""); resp.StatusCode != 200 {
		t.Fatalf("new admin: status %d, want 200", resp.StatusCode)
	}
	// The list marks the config-pinned admin.
	_, body := boss.do(http.MethodGet, "/api/admin/employees", "")
	if !strings.Contains(string(body), `"super_admin_pinned":true`) {
		t.Fatalf("pinned flag missing: %s", body)
	}
	// Revoke works; own role and pinned admins are refused.
	if resp, body := boss.do(http.MethodPost, path(peer.ID), `{"value":false}`); resp.StatusCode != 200 {
		t.Fatalf("revoke: %d %s", resp.StatusCode, body)
	}
	if e, _ := st.GetEmployee(ctx, peer.ID); e.IsSuperAdmin {
		t.Fatal("revoke did not stick")
	}
	if resp, _ := boss.do(http.MethodPost, path(me.ID), `{"value":false}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("self revoke: status %d, want 400", resp.StatusCode)
	}
	if resp, _ := boss.do(http.MethodPost, path(pinned.ID), `{"value":false}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pinned revoke: status %d, want 400", resp.StatusCode)
	}
	// Disabled employees cannot be granted.
	_ = st.SetEmployeeStatus(ctx, peer.ID, "disabled")
	if resp, _ := boss.do(http.MethodPost, path(peer.ID), `{"value":true}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("grant disabled: status %d, want 400", resp.StatusCode)
	}
	// Every change is audited.
	rows, _ := st.ListAudit(ctx, store.AuditFilter{})
	var grants, revokes int
	for _, a := range rows {
		switch a.Action {
		case "super_admin_grant":
			grants++
		case "super_admin_revoke":
			revokes++
		}
	}
	if grants != 1 || revokes != 1 {
		t.Fatalf("audit grants=%d revokes=%d, want 1/1", grants, revokes)
	}
}
