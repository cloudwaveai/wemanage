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

func TestPersonProfileUsesOnlyRequestedPersonAndEnforcesPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "profile-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "profile-admin")
	a, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "profile-a", Name: "甲"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "profile-b", Name: "乙"})
	if e != nil {
		t.Fatal(e)
	}
	boss, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "profile-boss", Name: "林伊默"})
	if e != nil {
		t.Fatal(e)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	if e = st.StartWorkDigestDay(ctx, day); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct {
		id   int64
		text string
	}{{a.ID, "甲的日报"}, {b.ID, "乙的日报"}, {boss.ID, "私密日报"}} {
		if e = st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: x.id, SummaryMD: x.text, Model: "test"}); e != nil {
			t.Fatal(e)
		}
	}
	if e = st.FinishWorkDigestDay(ctx, day, "", "test", "done", ""); e != nil {
		t.Fatal(e)
	}
	cookie, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	mock := summaryMock{called: make(chan string, 1)}
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
		v, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(v)
	}
	if code, _ := call("GET", fmt.Sprintf("/api/person-profile?employee_id=%d", boss.ID), ""); code != 404 {
		t.Fatalf("boss status %d", code)
	}
	path := fmt.Sprintf("/api/person-profile/generate?employee_id=%d", a.ID)
	if code, body := call("POST", path, `{"from":"2026-10-29","to":"2026-10-29"}`); code != 202 {
		t.Fatalf("%d %s", code, body)
	}
	select {
	case prompt := <-mock.called:
		if !strings.Contains(prompt, "工作画像") || !strings.Contains(prompt, "甲的日报") || strings.Contains(prompt, "乙的日报") || strings.Contains(prompt, "私密日报") {
			t.Fatal("wrong profile input")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no model call")
	}
	var body string
	for i := 0; i < 100; i++ {
		_, body = call("GET", fmt.Sprintf("/api/person-profile?employee_id=%d", a.ID), "")
		if strings.Contains(body, "项目进展") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "项目进展") || !strings.Contains(body, "甲的日报") {
		t.Fatalf("no profile %s", body)
	}
	_, body = call("GET", fmt.Sprintf("/api/person-profile?employee_id=%d", b.ID), "")
	if strings.Contains(body, "甲的日报") || strings.Contains(body, "项目进展") {
		t.Fatal("profile leaked to another person")
	}
	_, body = call("GET", "/api/custom-summary", "")
	if strings.Contains(body, "甲的日报") || strings.Contains(body, "项目进展") {
		t.Fatal("profile leaked into general summary history")
	}
	hiddenProject, e := st.CreateProject(ctx, "隐藏项目", "client")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_hidden_projects(project_id,reason) values($1,'test')`, hiddenProject.ID); e != nil {
		t.Fatal(e)
	}
	requestID := [16]byte{91}
	at := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	if e = st.InsertRequest(ctx, store.RequestRow{ID: requestID, StartedAt: at, EmployeeID: a.ID, ModelRequested: "test", Endpoint: "responses", ClientKind: "codex", StatusCode: 200}); e != nil {
		t.Fatal(e)
	}
	if _, e = st.AssignRequestProject(ctx, requestID, hiddenProject.ID, admin.ID); e != nil {
		t.Fatal(e)
	}
	_, body = call("GET", fmt.Sprintf("/api/person-profile?employee_id=%d", a.ID), "")
	if strings.Contains(body, "项目进展") || strings.Contains(body, "甲的日报") {
		t.Fatal("profile remains visible after source is assigned to hidden project")
	}
	if _, e = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	if code, _ := call("GET", fmt.Sprintf("/api/person-profile?employee_id=%d", a.ID), ""); code != 404 {
		t.Fatal("private profile remains accessible")
	}
}
