package wemanage_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestOrganizationDirectoryExcludesPrivateAndInactive(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "org-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "org-admin")
	parent, err := st.UpsertDepartment(ctx, store.Department{FeishuDeptID: "od-parent", Name: "父部门", LeaderOpenIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.UpsertDepartment(ctx, store.Department{FeishuDeptID: "od-child", ParentFeishuID: "od-parent", Name: "子部门", LeaderOpenIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "org-member", Name: "公开成员", Email: "secret@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "org-boss", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "org-disabled", Name: "停用成员", Status: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "org-private", Name: "私密成员"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, private.ID); err != nil {
		t.Fatal(err)
	}
	for _, x := range []int64{member.ID, boss.ID, disabled.ID, private.ID} {
		if err = st.SetEmployeeDepartments(ctx, x, []int64{parent, child}); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.Audit(ctx, store.AuditEntry{Action: "feishu_sync"}); err != nil {
		t.Fatal(err)
	}
	cookie, _, err := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	unauth, err := http.Get(srv.URL + "/api/organization")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous organization access: %d", unauth.StatusCode)
	}
	memberCookie, _, err := st.CreateWebSession(ctx, member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	memberReq, _ := http.NewRequest("GET", srv.URL+"/api/organization", nil)
	memberReq.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: memberCookie})
	denied, err := http.DefaultClient.Do(memberReq)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin organization access: %d", denied.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/organization", nil)
	req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || strings.Contains(string(body), "林伊默") || strings.Contains(string(body), "私密成员") || strings.Contains(string(body), "停用成员") || strings.Contains(string(body), "secret@example.com") {
		t.Fatalf("privacy failure %d %s", resp.StatusCode, body)
	}
	var got struct {
		Departments []struct {
			ID     int64  `json:"id"`
			Parent *int64 `json:"parent_id"`
		} `json:"departments"`
		Members []struct {
			ID          int64   `json:"id"`
			Departments []int64 `json:"department_ids"`
		} `json:"members"`
		SyncedAt *time.Time `json:"synced_at"`
	}
	if err = json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Departments) != 2 || got.SyncedAt == nil {
		t.Fatalf("missing structure or sync time: %s", body)
	}
	found := false
	for _, m := range got.Members {
		if m.ID == member.ID {
			found = true
			if len(m.Departments) != 2 {
				t.Fatalf("multi membership lost: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("member missing: %s", body)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/workspace?month=2026-10", nil)
	req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"range":"month"`) {
		t.Fatalf("month failed: %d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/workspace?month=2026-10&day=2026-10-01", nil)
	req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("conflicting dates accepted: %d", resp.StatusCode)
	}
}
