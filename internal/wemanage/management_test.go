package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagementLedgerOwnershipAndDelegatedReview(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if e := wemanage.Install(ctx, st); e != nil {
		t.Fatal(e)
	}
	if e := st.EnsureSuperAdmin(ctx, "mg-admin", "管理员"); e != nil {
		t.Fatal(e)
	}
	a, _ := st.GetEmployeeByOpenID(ctx, "mg-admin")
	m, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "mg-member", Name: "负责人"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "mg-other", Name: "其他人员"})
	if e != nil {
		t.Fatal(e)
	}
	login := func(id int64) user {
		c, token, e := st.CreateWebSession(ctx, id, time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		return user{c, token}
	}
	admin, member, outsider := login(a.ID), login(m.ID), login(other.ID)
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	call := func(u user, method, path string, body any, want int) string {
		t.Helper()
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		r.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: u.cookie})
		r.Header.Set("X-CSRF-Token", u.csrf)
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, res.StatusCode, want, raw)
		}
		return string(raw)
	}
	idOf := func(raw string) int64 {
		var d struct{ ID int64 }
		if e := json.Unmarshal([]byte(raw), &d); e != nil {
			t.Fatal(e)
		}
		return d.ID
	}
	var groupID int64
	if err := st.Pool.QueryRow(ctx, `insert into ip_groups(name,proxy_url_enc)values('cost-test','') returning id`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(ctx, store.Account{IPGroupID: groupID, CPAAuthName: "cost-source-test", Email: "must-not-expose@example.com", PlanType: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SaveSubscription(ctx, store.Subscription{AccountID: account.ID, Plan: "pro", Active: true}); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/api/cost-sources", nil, 403)
	source := call(admin, "GET", "/api/cost-sources", nil, 200)
	if !strings.Contains(source, `"observed_plan":"pro"`) || strings.Contains(source, "must-not-expose") || strings.Contains(source, "cost-source-test") {
		t.Fatalf("unexpected cost sources: %s", source)
	}
	if _, err = st.Pool.Exec(ctx, `insert into account_members(account_id,employee_id)values($1,$2)`, account.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	source = call(admin, "GET", "/api/cost-sources", nil, 200)
	if !strings.Contains(source, `"observed_plan":"pro"`) || strings.Contains(source, "其他人员") {
		t.Fatal("subscription missing or private member leaked", source)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=null where id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	cost := map[string]any{"month": "2026-11", "kind": "subscription", "currency": "CNY", "amount": "128.90", "label": "月订阅", "account_label": "账号 A", "seat_count": 1, "employee_ids": []int64{m.ID}}
	call(member, "POST", "/api/expenses", cost, 403)
	call(user{admin.cookie, "wrong"}, "POST", "/api/expenses", cost, 403)
	expense := idOf(call(admin, "POST", "/api/expenses", cost, 200))
	cost["id"] = expense
	cost["version"] = 1
	cost["amount"] = "139.10"
	call(admin, "POST", "/api/expenses", cost, 200)
	call(admin, "POST", "/api/expenses", cost, 409)
	b := call(admin, "GET", "/api/expenses", nil, 200)
	if !strings.Contains(b, "139.10") || strings.Contains(b, "128.90") {
		t.Fatal("latest cost", b)
	}
	b = call(admin, "GET", fmt.Sprintf("/api/expenses/%d/history", expense), nil, 200)
	if !strings.Contains(b, "128.90") || !strings.Contains(b, "139.10") {
		t.Fatal("cost history", b)
	}
	cost["version"] = 2
	cost["hidden"] = true
	call(admin, "POST", "/api/expenses", cost, 200)
	cost["version"] = 3
	cost["hidden"] = false
	call(admin, "POST", "/api/expenses", cost, 200)
	cost["amount"] = "NaN"
	call(admin, "POST", "/api/expenses", cost, 400)
	cost["amount"] = "1.00"
	cost["seat_count"] = 0
	call(admin, "POST", "/api/expenses", cost, 400)
	project := map[string]any{"name": "研发项目", "owner_id": m.ID, "status": "active", "start_day": "2026-11-01", "acceptance_day": "2026-11-20", "payments": []any{map[string]any{"title": "首款", "day": "2026-11-02", "amount": "2000.00", "currency": "CNY", "state": "received"}}}
	pid := idOf(call(admin, "POST", "/api/managed-projects", project, 200))
	b = call(member, "GET", "/api/managed-projects", nil, 200)
	if !strings.Contains(b, "研发项目") {
		t.Fatal(b)
	}
	if b = call(outsider, "GET", "/api/managed-projects", nil, 200); strings.Contains(b, "研发项目") {
		t.Fatal("owner leak", b)
	}
	project["id"] = pid
	project["version"] = 1
	project["status"] = "paused"
	call(outsider, "POST", "/api/managed-projects", project, 403)
	call(member, "POST", "/api/managed-projects", project, 200)
	call(member, "POST", "/api/managed-projects", project, 409)
	b = call(member, "GET", fmt.Sprintf("/api/managed-projects/%d/history", pid), nil, 200)
	if !strings.Contains(b, "paused") || !strings.Contains(b, "active") {
		t.Fatal("project history", b)
	}
	if b = call(outsider, "GET", fmt.Sprintf("/api/managed-projects/%d/history", pid), nil, 200); strings.Contains(b, "研发项目") {
		t.Fatal("history leak", b)
	}
	call(member, "GET", "/api/workspace", nil, 403)
	call(member, "GET", "/api/expenses", nil, 403)
	var run, item, asset int64
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status)values('daily','2026-11-01','2026-11-02','done')returning id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at)values($1,1,'2026-11-01',$2,'负责人','原始私有日报','test',now())`, run, m.ID); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_items(run_id,category,title,content,project_hint,evidence)values($1,'skill','方法','方法说明','研发项目','[]')returning id`, run).Scan(&item); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `insert into wm_assets(source_item_id,kind,author_id)values($1,'skill',$2)returning id`, item, a.ID).Scan(&asset); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_asset_versions(asset_id,version,title,content,state,editor_id)values($1,1,'审核方法','经过提炼的说明','draft',$2)`, asset, a.ID); e != nil {
		t.Fatal(e)
	}
	assignment := map[string]any{"action": "assign", "asset_id": asset, "version": 1, "reviewer_id": m.ID}
	call(member, "POST", "/api/review-tasks", assignment, 403)
	task := idOf(call(admin, "POST", "/api/review-tasks", assignment, 200))
	if b = call(outsider, "GET", "/api/review-tasks", nil, 200); strings.Contains(b, "经过提炼") {
		t.Fatal("review leak", b)
	}
	b = call(member, "GET", "/api/review-tasks", nil, 200)
	if !strings.Contains(b, "经过提炼") || strings.Contains(b, "原始私有日报") {
		t.Fatal("scope", b)
	}
	review := map[string]any{"id": task, "action": "approved", "note": "操作步骤和停止条件已核对"}
	call(outsider, "POST", "/api/review-tasks", review, 403)
	review["action"] = "deferred"
	call(member, "POST", "/api/review-tasks", review, 200)
	review["action"] = "approved"
	call(member, "POST", "/api/review-tasks", review, 200)
	call(member, "POST", "/api/review-tasks", review, 409)
	if b = call(outsider, "GET", "/api/shared/assets", nil, 200); !strings.Contains(b, "经过提炼") {
		t.Fatal("not published", b)
	}
	if _, e = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, m.ID); e != nil {
		t.Fatal(e)
	}
	if b = call(admin, "GET", "/api/review-tasks", nil, 200); strings.Contains(b, "经过提炼") {
		t.Fatal("private review leak", b)
	}
	if b = call(outsider, "GET", "/api/shared/assets", nil, 200); strings.Contains(b, "经过提炼") {
		t.Fatal("private publication leak", b)
	}
}
