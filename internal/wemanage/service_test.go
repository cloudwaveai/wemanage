package wemanage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aiseat/internal/feishu"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"
	"aiseat/internal/wemanage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type user struct{ cookie, csrf string }

func TestCaseLifecycleAndPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "ou_manager", "manager"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "ou_boss", "林伊默"); err != nil {
		t.Fatal(err)
	}
	manager, err := st.GetEmployeeByOpenID(ctx, "ou_manager")
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.GetEmployeeByOpenID(ctx, "ou_boss")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_member", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	login := func(id int64) user {
		cookie, csrf, err := st.CreateWebSession(ctx, id, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return user{cookie, csrf}
	}
	admin, bossUser, regular := login(manager.ID), login(boss.ID), login(member.ID)
	project, err := st.CreateProject(ctx, "项目", "client")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		employee int64
		model    string
	}{{member.ID, "公开模型"}, {boss.ID, "私密模型"}} {
		requestID := uuid.New()
		if err := st.InsertRequest(ctx, store.RequestRow{ID: requestID, StartedAt: time.Now(), EmployeeID: item.employee, ModelRequested: item.model, Endpoint: "responses", ClientKind: "codex", StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AssignRequestProject(ctx, requestID, project.ID, manager.ID); err != nil {
			t.Fatal(err)
		}
	}
	event, err := st.CreateProjectEvent(ctx, store.ProjectEvent{ProjectID: project.ID, EmployeeID: member.ID, OccurredAt: time.Now(), Content: "正常证据", Evidence: "成果链接"}, manager.ID)
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.CreateProjectEvent(ctx, store.ProjectEvent{ProjectID: project.ID, EmployeeID: boss.ID, OccurredAt: time.Now(), Content: "隐私证据"}, boss.ID)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer ts.Close()
	call := func(u user, method, path, body string, csrf bool) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewBufferString(body))
		if u.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: u.cookie})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", u.csrf)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	if status, _ := call(user{}, "GET", "/api/projects", "", false); status != 401 {
		t.Fatalf("anonymous: %d", status)
	}
	if status, _ := call(regular, "GET", "/api/projects", "", false); status != 403 {
		t.Fatalf("non-admin: %d", status)
	}
	if status, body := call(admin, "GET", fmt.Sprintf("/api/projects/%d/activity", project.ID), "", false); status != 200 || !strings.Contains(body, "公开模型") || strings.Contains(body, "私密模型") {
		t.Fatalf("activity privacy: %d %s", status, body)
	}
	if status, body := call(admin, "GET", "/api/reporting/usage", "", false); status != 200 || !strings.Contains(body, "公开模型") || strings.Contains(body, "私密模型") {
		t.Fatalf("usage privacy: %d %s", status, body)
	}
	if status, _ := call(user{}, "GET", "/api/reporting/usage", "", false); status != 401 {
		t.Fatalf("usage anonymous: %d", status)
	}
	if status, _ := call(admin, "GET", "/api/reporting/usage?month=invalid", "", false); status != 400 {
		t.Fatalf("usage invalid month: %d", status)
	}
	if status, _ := call(user{}, "GET", "/api/log-worker", "", false); status != 401 {
		t.Fatalf("worker anonymous: %d", status)
	}
	if status, _ := call(admin, "PUT", "/api/log-worker", `{"enabled":true}`, false); status != 403 {
		t.Fatalf("worker csrf: %d", status)
	}
	if status, body := call(admin, "PUT", "/api/log-worker", `{"enabled":true}`, true); status != 200 || !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("worker enable: %d %s", status, body)
	}
	if status, body := call(admin, "PUT", "/api/log-worker", `{"enabled":false}`, true); status != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("worker pause: %d %s", status, body)
	}
	input := func(e int64) string {
		return fmt.Sprintf(`{"project_id":%d,"event_id":%d,"title":"可复用案例","situation":"背景","approach":"方法","outcome":"结果"}`, project.ID, e)
	}
	if status, _ := call(admin, "POST", "/api/cases", input(event.ID), false); status != 403 {
		t.Fatalf("csrf: %d", status)
	}
	if status, _ := call(admin, "POST", "/api/cases", input(private.ID), true); status != 404 {
		t.Fatalf("private event: %d", status)
	}
	status, body := call(admin, "POST", "/api/cases", input(event.ID), true)
	if status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(admin, "POST", fmt.Sprintf("/api/cases/%d/feedback", out.ID), `{"content":"草稿反馈"}`, true); status != 404 {
		t.Fatalf("draft feedback: %d", status)
	}
	if status, body := call(admin, "POST", fmt.Sprintf("/api/cases/%d/confirm", out.ID), `{}`, true); status != 200 {
		t.Fatalf("confirm: %d %s", status, body)
	}
	if status, body := call(admin, "POST", fmt.Sprintf("/api/cases/%d/feedback", out.ID), `{"content":"使用结果"}`, true); status != 200 {
		t.Fatalf("feedback: %d %s", status, body)
	}
	if status, body := call(admin, "GET", "/api/cases", "", false); status != 200 || !strings.Contains(body, "使用结果") || !strings.Contains(body, "正常证据") {
		t.Fatalf("list: %d %s", status, body)
	}
	if status, body := call(admin, "POST", fmt.Sprintf("/api/cases/%d/edit", out.ID), `{"title":"修正案例","situation":"背景2","approach":"方法2","outcome":"结果2"}`, true); status != 200 {
		t.Fatalf("edit: %d %s", status, body)
	}
	var oldTitle, oldState string
	if err := st.Pool.QueryRow(ctx, `SELECT title,state FROM wm_case_revisions WHERE case_id=$1`, out.ID).Scan(&oldTitle, &oldState); err != nil || oldTitle != "可复用案例" || oldState != "confirmed" {
		t.Fatalf("revision: %s %s %v", oldTitle, oldState, err)
	}
	if status, body := call(admin, "GET", "/api/cases", "", false); status != 200 || !strings.Contains(body, "修正案例") || !strings.Contains(body, "使用结果") || !strings.Contains(body, `"state":"draft"`) {
		t.Fatalf("edited case: %d %s", status, body)
	}
	if status, body := call(admin, "POST", fmt.Sprintf("/api/cases/%d/confirm", out.ID), `{}`, true); status != 200 {
		t.Fatalf("reconfirm: %d %s", status, body)
	}
	// A private creator's case based on another employee's event is private too.
	status, body = call(bossUser, "POST", "/api/cases", input(event.ID), true)
	if status != 200 {
		t.Fatalf("boss create: %d %s", status, body)
	}
	var privateCase struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &privateCase)
	if status, _ := call(admin, "POST", fmt.Sprintf("/api/cases/%d/confirm", privateCase.ID), `{}`, true); status != 404 {
		t.Fatalf("private author mutation: %d", status)
	}
	status, body = call(admin, "GET", "/api/cases", "", false)
	var listed struct {
		Cases []struct {
			ID int64 `json:"id"`
		} `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &listed); status != 200 || err != nil || len(listed.Cases) != 1 || listed.Cases[0].ID != out.ID {
		t.Fatalf("private author leaked: %d %s", status, body)
	}
	if status, body := call(bossUser, "POST", fmt.Sprintf("/api/cases/%d/edit", out.ID), `{"title":"私密修改","situation":"私密背景","approach":"方法","outcome":"结果"}`, true); status != 200 {
		t.Fatalf("private editor: %d %s", status, body)
	}
	if status, body := call(admin, "GET", "/api/cases", "", false); status != 200 || strings.Contains(body, "私密修改") || strings.Contains(body, "私密背景") {
		t.Fatalf("private editor leaked: %d %s", status, body)
	}
	if status, _ := call(admin, "POST", fmt.Sprintf("/api/cases/%d/confirm", out.ID), `{}`, true); status != 404 {
		t.Fatalf("private editor mutation: %d", status)
	}
	// Visibility is checked at read and mutation time, including after policy changes.
	if _, err := st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	if status, body := call(admin, "GET", "/api/cases", "", false); status != 200 || strings.Contains(body, "正常证据") {
		t.Fatalf("newly private source leaked: %d %s", status, body)
	}
	if status, _ := call(admin, "POST", fmt.Sprintf("/api/cases/%d/feedback", out.ID), `{"content":"不允许"}`, true); status != 404 {
		t.Fatalf("private source mutation: %d", status)
	}
}

// We-Manage reuses the gateway's Feishu OAuth flow and shared session.
func TestFeishuLoginReturnsToWorkspace(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "ou_feishu_workspace", "管理员"); err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/authen/v2/oauth/token":
			_, _ = io.WriteString(w, `{"code":0,"access_token":"test-user-token"}`)
		case "/open-apis/authen/v1/user_info":
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_feishu_workspace","name":"管理员"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	feishuClient := feishu.NewClient(mock.URL, "test-app", "test-secret", mock.Client())
	feishuClient.SetAuthBase(mock.URL)
	gateway := web.NewServer(web.Deps{Store: st, Feishu: feishuClient, PublicBaseURL: "http://gateway.test"})
	router := chi.NewRouter()
	gateway.Mount(router)
	gatewayHTTP := httptest.NewServer(router)
	defer gatewayHTTP.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(gatewayHTTP.URL + "/api/auth/feishu/login?next=%2Fwemanager%2F")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	authURL, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("missing OAuth state")
	}
	req, _ := http.NewRequest("GET", gatewayHTTP.URL+"/api/auth/feishu/callback?code=ok&state="+url.QueryEscape(state), nil)
	for _, cookie := range resp.Cookies() {
		req.AddCookie(cookie)
	}
	callback, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	callback.Body.Close()
	if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != "/wemanager/" {
		t.Fatalf("callback %d location %q", callback.StatusCode, callback.Header.Get("Location"))
	}
	var session *http.Cookie
	for _, c := range callback.Cookies() {
		if c.Name == "aiseat_sess" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("missing gateway session")
	}
	workspace := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer workspace.Close()
	req, _ = http.NewRequest("GET", workspace.URL+"/api/session", nil)
	req.AddCookie(session)
	got, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	data, _ := io.ReadAll(got.Body)
	if got.StatusCode != 200 || !strings.Contains(string(data), "管理员") {
		t.Fatalf("workspace session %d %s", got.StatusCode, data)
	}
}
