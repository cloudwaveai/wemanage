package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
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

func TestWorkspaceAggregatesExcludePrivateAndHidden(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if e := wemanage.Install(ctx, st); e != nil {
		t.Fatal(e)
	}
	if e := st.EnsureSuperAdmin(ctx, "workspace-admin", "管理员"); e != nil {
		t.Fatal(e)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "workspace-admin")
	person, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "workspace-person", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	boss, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "workspace-boss", Name: "林伊默"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := st.CreateProject(ctx, "隐藏项目", "client")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_hidden_projects(project_id,reason) values($1,'test')`, p.ID); e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 10, 10, 1, 30, 0, 0, time.UTC)
	session := int64(17)
	for n, emp := range []int64{person.ID, boss.ID, person.ID} {
		id := [16]byte{byte(n + 1)}
		if e = st.InsertRequest(ctx, store.RequestRow{ID: id, StartedAt: at, EmployeeID: emp, SessionID: &session, ClientKind: "codex", Endpoint: "responses", ModelRequested: "model", StatusCode: 200, TokensInput: 100, TokensOutput: 10}); e != nil {
			t.Fatal(e)
		}
		if n == 2 {
			if _, e = st.AssignRequestProject(ctx, id, p.ID, admin.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if e = st.StartWorkDigestDay(ctx, day); e != nil {
		t.Fatal(e)
	}
	if e = st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: person.ID, SummaryMD: "成员工作摘要", Model: "test"}); e != nil {
		t.Fatal(e)
	}
	if e = st.FinishWorkDigestDay(ctx, day, "", "test", "done", ""); e != nil {
		t.Fatal(e)
	}
	var publicRun, privateRun int64
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status) values('daily','2026-10-10','2026-10-11','done') returning id`).Scan(&publicRun); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status,version) values('daily','2026-10-10','2026-10-11','done',2) returning id`).Scan(&privateRun); e != nil {
		t.Fatal(e)
	}
	for _, run := range []int64{publicRun, privateRun} {
		if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,1,'2026-10-10',$2,'成员','摘要','test',now())`, run, person.ID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,2,'2026-10-10',$2,'林伊默','私密','test',now())`, privateRun, boss.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_items(run_id,category,title,content,evidence) values($1,'practice','公开方法','可见','[{"source":1}]'),($2,'practice','混合私密方法','不可见','[{"source":1}]')`, publicRun, privateRun); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_items(run_id,category,title,content,evidence,reviewed_by,reviewed_content) values($1,'practice','私密审核内容','原文','[{"source":1}]',$2,'老板内容')`, publicRun, boss.ID); e != nil {
		t.Fatal(e)
	}
	cookie, _, e := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	call := func(path string) (int, []byte) {
		r, _ := http.NewRequest("GET", srv.URL+path, nil)
		r.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	code, b := call("/api/workspace?day=2026-10-10")
	var result struct {
		Rows []struct {
			Employee string   `json:"employee"`
			Requests int      `json:"requests"`
			Cost     *float64 `json:"cost"`
			Priced   int      `json:"priced"`
		} `json:"rows"`
		People []struct {
			Sessions int `json:"sessions"`
		} `json:"people"`
	}
	if e = json.Unmarshal(b, &result); e != nil || code != 200 || len(result.Rows) != 1 || result.Rows[0].Employee != "成员" || result.Rows[0].Requests != 1 || result.Rows[0].Cost != nil || result.Rows[0].Priced != 0 || len(result.People) != 1 || result.People[0].Sessions != 1 {
		t.Fatalf("status=%d result=%s err=%v", code, b, e)
	}
	code, b = call(fmt.Sprintf("/api/workspace?day=2026-10-10&employee_id=%d", person.ID))
	if code != 200 || (!strings.Contains(string(b), "成员工作摘要") || !strings.Contains(string(b), "公开方法") || (strings.Contains(string(b), "混合私密方法") || strings.Contains(string(b), "老板内容"))) {
		t.Fatalf("digest %d %s", code, b)
	}
	code, _ = call(fmt.Sprintf("/api/workspace?day=2026-10-10&employee_id=%d", boss.ID))
	if code != 404 {
		t.Fatalf("private status %d", code)
	}
}
