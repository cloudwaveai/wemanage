package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workdigest"
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

func TestProjectKnowledgeCrossDayIsolationAndReuse(t *testing.T) {
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
	mock := depthMock{called: make(chan string, 4)}
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
	if _, err = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,2,'2026-10-30',$2,'成员','项目甲已完成上线核验，之前的上线待办已解决','test',now());`, run, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `update wm_knowledge_items set evidence='[{"source":1,"quote":"项目甲已经提交验收"},{"source":2,"quote":"项目甲已完成上线核验"}]' where id=$1`, item); err != nil {
		t.Fatal(err)
	}
	path := "/api/project-report?project=项目甲&kind=knowledge"
	if code, body := call("POST", "/api/project-report/generate?project=项目甲&kind=knowledge", `{"from":"2026-10-29","to":"2026-10-30"}`); code != 202 {
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
	if !strings.Contains(body, "两日项目知识") || !strings.Contains(body, "尚未确认上线") {
		t.Fatal(body)
	}
	if code, b := call("POST", "/api/project-report/generate?project=项目甲&kind=knowledge", `{"from":"2026-10-29","to":"2026-10-30"}`); code != 200 || !strings.Contains(b, `"reused":true`) {
		t.Fatal(code, b)
	}
	var reportID int64
	if err = st.Pool.QueryRow(ctx, `select id from wm_custom_summaries where analysis_kind='knowledge' and state='done'`).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if code, b := call("POST", fmt.Sprintf("/api/project-report/%d/archive", reportID), `{}`); code != 200 {
			t.Fatal(code, b)
		}
	}
	var versions int
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_asset_versions`).Scan(&versions); err != nil || versions != 1 {
		t.Fatal("archive not idempotent", versions, err)
	}
	if _, b := call("GET", "/api/assets", ""); !strings.Contains(b, "两日项目知识") {
		t.Fatal("knowledge not archived", b)
	}
	if _, b := call("GET", "/api/project-report?project=项目甲", ""); strings.Contains(b, "两日项目知识") {
		t.Fatal("knowledge leaked into progress history")
	}
	_, body = call("GET", "/api/custom-summary", "")
	if strings.Contains(body, "两日项目知识") {
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
	if _, b := call("GET", "/api/assets", ""); strings.Contains(b, "两日项目知识") {
		t.Fatal("private derived asset leaked")
	}
	var count int
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_custom_summary_sources`).Scan(&count); err != nil || count != 2 {
		t.Fatal(fmt.Sprintf("source history lost %d %v", count, err))
	}
}

type depthMock struct{ called chan string }

func (m depthMock) ExtractWithModel(_ context.Context, model, system, material string) (string, string, workdigest.ModelUsage, error) {
	m.called <- system + material
	if strings.Contains(system, "证据账本，供后续") {
		return `{"facts":[{"id":"F1","statement":"等待上线","date":"2026-10-29","status":"historical","evidence":[{"source":1,"quote":"尚未确认上线"}]},{"id":"F2","statement":"完成上线核验","date":"2026-10-30","status":"resolved","evidence":[{"source":2,"quote":"项目甲已完成上线核验"}]}]}`, model, workdigest.ModelUsage{}, nil
	}
	return `{"overview":"两日项目知识","knowledge":[{"title":"上线验证","body":"已由等待进入完成核验","status":"current","fact_ids":["F1","F2"]}],"issues":[{"title":"上线待办","body":"已核验","status":"resolved","fact_ids":["F2"]}],"skills":[],"gaps":["缺少具体验证步骤"]}`, model, workdigest.ModelUsage{}, nil
}
