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

func TestProjectReportSourcesIsolationAndHistory(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "pr-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "pr-admin")
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "pr-member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	var run, item int64
	if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status,version) values('daily','2026-10-29','2026-10-30','done',1) returning id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,1,'2026-10-29',$2,'成员','项目甲已经提交验收；尚未确认上线','test',now())`, run, member.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_items(run_id,category,title,content,project_hint,evidence) values($1,'project','验收','等待验收','项目甲','[{"source":1,"quote":"项目甲已经提交验收"}]') returning id`, run).Scan(&item); err != nil {
		t.Fatal(err)
	}
	cookie, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	mock := summaryMock{called: make(chan string, 2)}
	srv := httptest.NewServer((&wemanage.Service{Store: st, Summarizer: mock}).Handler())
	defer srv.Close()
	call := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
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
	path := "/api/project-report?project=项目甲"
	if code, body := call("POST", "/api/project-report/generate?project=项目甲", `{"from":"2026-10-29","to":"2026-10-29"}`); code != 202 {
		t.Fatalf("%d %s", code, body)
	}
	select {
	case material := <-mock.called:
		if !strings.Contains(material, "尚未确认上线") || !strings.Contains(material, "目标项目：项目甲") {
			t.Fatal("wrong project material")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no model call")
	}
	var body string
	for i := 0; i < 100; i++ {
		_, body = call("GET", path, "")
		if strings.Contains(body, `"state":"done"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "项目进展") || !strings.Contains(body, "尚未确认上线") {
		t.Fatal(body)
	}
	_, body = call("GET", "/api/custom-summary", "")
	if strings.Contains(body, "项目进展") {
		t.Fatal("project leaked into general summaries")
	}
	if code, _ := call("GET", "/api/project-report?project=不存在", ""); code != 404 {
		t.Fatal(code)
	}
	if _, err = st.Pool.Exec(ctx, `update wm_knowledge_items set status='rejected' where id=$1`, item); err != nil {
		t.Fatal(err)
	}
	if code, body := call("GET", path, ""); code != 404 || strings.Contains(body, "尚未确认上线") {
		t.Fatal("rejected project visible", code, body)
	}
	if _, err = st.Pool.Exec(ctx, `update wm_knowledge_items set status='draft' where id=$1`, item); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("GET", path, ""); code != 404 {
		t.Fatal("private sources visible", code)
	}
	var count int
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_custom_summary_sources`).Scan(&count); err != nil || count != 1 {
		t.Fatal(fmt.Sprintf("source history lost %d %v", count, err))
	}
}
