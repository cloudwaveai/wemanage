package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAssetsPublicationPermissionsAndVersions(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "asset-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "asset-admin")
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "asset-member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	ac, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	mc, _, _ := st.CreateWebSession(ctx, member.ID, time.Hour)
	var run, item int64
	if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status,version) values('daily','2026-11-01','2026-11-02','done',1) returning id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,1,'2026-11-01',$2,'成员','不应公开的原始日报','test',now())`, run, member.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_items(run_id,category,title,content,project_hint,evidence) values($1,'project','项目线索','工作事实','甲','[]') returning id`, run).Scan(&item); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	call := func(cookie, method, path, body, token string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		}
		req.Header.Set("X-CSRF-Token", token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	save := func(version int, state string) (int, string) {
		return call(ac, "POST", "/api/assets", fmt.Sprintf(`{"source_item_id":%d,"kind":"skill","title":"可复用方法","content":"## 验证\n已核对的方法","state":%q,"version":%d}`, item, state, version), csrf)
	}
	if code, _ := call("", "GET", "/api/shared/assets", "", ""); code != 401 {
		t.Fatal(code)
	}
	if code, _ := call(mc, "GET", "/api/assets", "", ""); code != 403 {
		t.Fatal(code)
	}
	if code, _ := call(mc, "GET", "/api/session", "", ""); code != 200 {
		t.Fatal(code)
	}
	if code, _ := call(ac, "POST", "/api/assets", "{}", ""); code != 403 {
		t.Fatal(code)
	}
	if code, b := save(0, "draft"); code != 200 {
		t.Fatal(code, b)
	}
	if _, b := call(mc, "GET", "/api/shared/assets", "", ""); strings.Contains(b, "可复用方法") {
		t.Fatal("draft leaked")
	}
	if code, b := save(1, "published"); code != 200 {
		t.Fatal(code, b)
	}
	if _, b := call(mc, "GET", "/api/shared/assets", "", ""); !strings.Contains(b, "可复用方法") || strings.Contains(b, "不应公开") {
		t.Fatal(b)
	}
	if code, _ := save(1, "draft"); code != 409 {
		t.Fatal("lost update", code)
	}
	if code, b := save(2, "hidden"); code != 200 {
		t.Fatal(code, b)
	}
	if _, b := call(mc, "GET", "/api/shared/assets", "", ""); strings.Contains(b, "可复用方法") {
		t.Fatal("withdrawal failed")
	}
	if code, b := save(3, "published"); code != 200 {
		t.Fatal(code, b)
	}
	if code, b := call(ac, "POST", "/api/assets/seed", "{}", csrf); code != 200 || !strings.Contains(b, `"created":1`) {
		t.Fatal(code, b)
	}
	if code, b := call(ac, "POST", "/api/assets/seed", "{}", csrf); code != 200 || !strings.Contains(b, `"created":0`) {
		t.Fatal("seed duplicated", code, b)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, b := call(mc, "GET", "/api/shared/assets", "", ""); strings.Contains(b, "可复用方法") {
		t.Fatal("private source leaked")
	}
	if code, _ := save(4, "published"); code != 404 {
		t.Fatal(code)
	}
	var n int
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_asset_versions v join wm_assets a on a.id=v.asset_id where a.kind='skill'`).Scan(&n); err != nil || n != 4 {
		t.Fatal("versions not retained", n, err)
	}
}
