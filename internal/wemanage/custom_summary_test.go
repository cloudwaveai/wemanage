package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workdigest"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type summaryMock struct{ called chan string }

func (m summaryMock) ExtractWithModel(_ context.Context, model, system, material string) (string, string, workdigest.ModelUsage, error) {
	m.called <- system + "\n" + material
	return "项目进展［来源 1］", model, workdigest.ModelUsage{}, nil
}
func TestCustomSummaryIsPersonalAndExcludesPrivateSources(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "custom-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "custom-admin")
	if err := st.EnsureSuperAdmin(ctx, "custom-other", "其他管理员"); err != nil {
		t.Fatal(err)
	}
	other, _ := st.GetEmployeeByOpenID(ctx, "custom-other")
	public, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "custom-member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "custom-boss", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	if err = st.StartWorkDigestDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		id   int64
		text string
	}{{public.ID, "公开日报内容"}, {private.ID, "不能看到的内容"}} {
		if err = st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: x.id, SummaryMD: x.text, Model: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.FinishWorkDigestDay(ctx, day, "", "test", "done", ""); err != nil {
		t.Fatal(err)
	}
	cookie, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	otherCookie, _, _ := st.CreateWebSession(ctx, other.ID, time.Hour)
	mock := summaryMock{called: make(chan string, 1)}
	srv := httptest.NewServer((&wemanage.Service{Store: st, Summarizer: mock}).Handler())
	defer srv.Close()
	call := func(method, path, body, session, token string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: session})
		req.Header.Set("X-CSRF-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := call("PUT", "/api/custom-summary", `{"prompt":"突出风险"}`, cookie, csrf); code != 200 {
		t.Fatal(code)
	}
	if code, b := call("POST", "/api/custom-summary/generate", `{"from":"2026-10-29","to":"2026-10-29","prompt":"突出风险"}`, cookie, csrf); code != 202 {
		t.Fatalf("%d %s", code, b)
	}
	select {
	case body := <-mock.called:
		if !strings.Contains(body, "公开日报内容") || !strings.Contains(body, "突出风险") || strings.Contains(body, "不能看到的内容") {
			t.Fatal("model received incorrect source or prompt")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("model was not called")
	}
	var body string
	for i := 0; i < 100; i++ {
		_, body = call("GET", "/api/custom-summary", "", cookie, "")
		if strings.Contains(body, "项目进展") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "项目进展") || !strings.Contains(body, "公开日报内容") || strings.Contains(body, "不能看到的内容") {
		t.Fatalf("unexpected history %s", body)
	}
	_, body = call("GET", "/api/custom-summary", "", otherCookie, "")
	if strings.Contains(body, "项目进展") || strings.Contains(body, "突出风险") {
		t.Fatal("report or prompt leaked between admins")
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, public.ID); err != nil {
		t.Fatal(err)
	}
	_, body = call("GET", "/api/custom-summary", "", cookie, "")
	if strings.Contains(body, "项目进展") || strings.Contains(body, "公开日报内容") {
		t.Fatal("report remained visible after source became private")
	}
}
