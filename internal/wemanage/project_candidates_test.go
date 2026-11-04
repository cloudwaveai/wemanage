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

func TestProjectCandidatesMergeHideAndPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "p-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "p-admin")
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "p-member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "p-boss", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	var publicRun, privateRun int64
	for i := 0; i < 2; i++ {
		var id int64
		if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status,version) values('daily','2026-10-29','2026-10-30','done',$1) returning id`, i+1).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			publicRun = id
		} else {
			privateRun = id
		}
	}
	for _, x := range []struct {
		run, emp int64
		name     string
	}{{publicRun, member.ID, "成员"}, {privateRun, boss.ID, "林伊默"}} {
		_, err = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,1,'2026-10-29',$2,$3,'日报','test',now())`, x.run, x.emp, x.name)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		run  int64
		name string
	}{{publicRun, "项目甲"}, {publicRun, "甲项目"}, {privateRun, "私密项目"}} {
		_, err = st.Pool.Exec(ctx, `insert into wm_knowledge_items(run_id,category,title,content,project_hint,evidence) values($1,'project','进展','具体内容',$2,'[{"source":1,"quote":"日报"}]')`, x.run, x.name)
		if err != nil {
			t.Fatal(err)
		}
	}
	cookie, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	call := func(method, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+"/api/project-candidates", bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		req.Header.Set("X-CSRF-Token", csrf)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := call("GET", "")
	if code != 200 || !strings.Contains(body, "项目甲") || !strings.Contains(body, "甲项目") || strings.Contains(body, "私密项目") {
		t.Fatalf("unexpected projects: %d %s", code, body)
	}
	code, body = call("PUT", `{"action":"merge","name":"甲项目","target":"项目甲"}`)
	if code != 200 {
		t.Fatal(code, body)
	}
	_, body = call("GET", "")
	if strings.Count(body, `"name":"项目甲"`) != 1 || !strings.Contains(body, `"aliases":["甲项目"]`) {
		t.Fatal(body)
	}
	for _, action := range []string{"hide", "restore"} {
		code, body = call("PUT", fmt.Sprintf(`{"action":%q,"name":"项目甲"}`, action))
		if code != 200 {
			t.Fatal(code, body)
		}
		_, body = call("GET", "")
		want := `"hidden":true`
		if action == "restore" {
			want = `"hidden":false`
		}
		if !strings.Contains(body, want) {
			t.Fatal(body)
		}
	}
}
