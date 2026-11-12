package wemanage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"github.com/google/uuid"
)

func TestDiagnosticHTTPPrivacyAndPaging(t *testing.T) {
	// This suite must never connect to the production gateway database.
	if !strings.Contains(testutil.TestDBURL(), "/wemanage_diagnostics_test") {
		t.Fatal("requires dedicated wemanage_diagnostics_test database")
	}
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := st.Pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	employee := func(key, name string) store.Employee {
		x, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: key, Name: name})
		if e != nil {
			t.Fatal(e)
		}
		return x
	}
	admin := employee("diag-admin", "测试管理员")
	exec(`update employees set is_super_admin=true where id=$1`, admin.ID)
	a := employee("diag-a", "测试成员 A")
	private := employee("diag-private", "私密成员")
	hidden := employee("diag-hidden", "隐藏项目成员")
	exec(`update employees set private_work_since=now() where id=$1`, private.ID)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	at := start.Add(10 * time.Hour)
	insert := func(emp int64, sid int, model string, input, cached, output, status int, errorType string, when time.Time) string {
		id := uuid.NewString()
		exec(`insert into requests(id,employee_id,session_id,started_at,client_kind,endpoint,model_requested,tokens_input,tokens_cached,tokens_output,status_code,error_type) values($1,$2,$3,$4,'codex','responses',$5,$6,$7,$8,$9,$10)`, id, emp, sid, when, model, input, cached, output, status, errorType)
		return id
	}
	source := insert(a.ID, 101, "gpt-6-astra", 100, 90, 20, 200, "", at)
	insert(a.ID, 102, "gpt-6.1-sol", 200, 100, 30, 200, "request_timeout", at.Add(time.Hour))
	insert(a.ID, 101, "gpt-6-astra", 0, 0, 0, 502, "upstream_unreachable", at.Add(2*time.Hour))
	insert(a.ID, 101, "gpt-6-astra", 50, 40, 5, 200, "", at.AddDate(0, 0, -1))
	insert(private.ID, 201, "private-model", 999, 0, 1, 200, "", at)
	hiddenReq := insert(hidden.ID, 301, "hidden-model", 888, 0, 1, 200, "", at)
	var project int64
	if e := st.Pool.QueryRow(ctx, `insert into projects(name,category) values('hidden','infrastructure') returning id`).Scan(&project); e != nil {
		t.Fatal(e)
	}
	exec(`insert into request_projects(request_id,project_id)values($1,$2)`, hiddenReq, project)
	exec(`insert into wm_hidden_projects(project_id,reason)values($1,'test')`, project)
	for i := 0; i < 60; i++ {
		exec(`insert into request_texts(request_id,employee_id,session_id,started_at,role,text)values($1,$2,101,$3,'user',$4)`, source, a.ID, at, fmt.Sprintf("第%d条证据：避免杀掉付费绘图 <script>alert('x')</script>", i))
	}
	for i := 0; i < 102; i++ {
		exec(`insert into risk_events(employee_id,rule,severity,detail,at)values($1,'content_flagged','warn','{"rule":"av-evasion","blocked":false,"snippet":"避免杀掉付费绘图 <script>不应执行</script>"}',$2)`, a.ID, at)
	}
	for _, emp := range []int64{private.ID, hidden.ID} {
		exec(`insert into risk_events(employee_id,rule,severity,detail,at)values($1,'content_flagged','warn','{"snippet":"不可泄露"}',$2)`, emp, at)
	}
	cookie, _, err := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	memberCookie, _, err := st.CreateWebSession(ctx, a.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service := (&wemanage.Service{Store: st}).Handler()
	mux := http.NewServeMux()
	mux.Handle("/", service)
	// Optional loopback-only synthetic preview; never compiled into the service.
	preview := os.Getenv("WM_DIAGNOSTIC_PREVIEW_FILE")
	if preview != "" {
		mux.HandleFunc("/__fixture_login", func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "aiseat_sess", Value: cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, "/#token-analysis", 302)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func(path, auth string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if auth != "" {
			req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: auth})
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var result map[string]any
		if e = json.Unmarshal(b, &result); e != nil {
			t.Fatal(string(b))
		}
		return resp.StatusCode, result
	}
	period := "?from=" + start.Format(time.DateOnly) + "&to=" + start.Format(time.DateOnly)
	for _, path := range []string{"/api/usage-analysis", "/api/risk-analysis", "/api/usage-analysis/evidence"} {
		for _, test := range []struct {
			auth   string
			status int
		}{{"", 401}, {memberCookie, 403}} {
			code, _ := get(path+period, test.auth)
			if code != test.status {
				t.Fatalf("access %s %d", path, code)
			}
		}
	}
	code, j := get("/api/usage-analysis"+period, cookie)
	if code != 200 {
		t.Fatal(code, j)
	}
	var total map[string]any
	models := map[string]bool{}
	for _, v := range j["rows"].([]any) {
		x := v.(map[string]any)
		if x["kind"] == "total" {
			total = x
		}
		if x["kind"] == "model" {
			models[x["key"].(string)] = true
		}
	}
	for k, want := range map[string]float64{"requests": 3, "input": 300, "cached": 190, "output": 50, "uncached": 110, "errors": 2, "error_tokens": 230} {
		if total[k] != want {
			t.Fatalf("%s=%v want %v", k, total[k], want)
		}
	}
	if len(models) != 2 || models["private-model"] || models["hidden-model"] {
		t.Fatal(models)
	}
	if j["previous"].(map[string]any)["input"] != float64(50) {
		t.Fatal("wrong previous period", j["previous"])
	}
	code, _ = get("/api/usage-analysis"+period+fmt.Sprintf("&employee_id=%d", private.ID), cookie)
	if code != 404 {
		t.Fatal("private person", code)
	}
	code, j = get("/api/risk-analysis"+period, cookie)
	if code != 200 || len(j["events"].([]any)) != 100 {
		t.Fatal(code, j)
	}
	for _, v := range j["events"].([]any) {
		x := v.(map[string]any)
		if x["employee_id"] != float64(a.ID) || x["blocked"] != false || !strings.Contains(x["hint"].(string), "疑似") {
			t.Fatal(x)
		}
	}
	code, j = get("/api/risk-analysis"+period+fmt.Sprintf("&cursor=%.0f", j["next_cursor"]), cookie)
	if code != 200 || len(j["events"].([]any)) != 2 || j["next_cursor"] != float64(0) {
		t.Fatal("risk paging", code, j)
	}
	evidencePath := "/api/usage-analysis/evidence" + period + fmt.Sprintf("&employee_id=%d&session_id=101", a.ID)
	code, j = get(evidencePath, cookie)
	if code != 200 || len(j["items"].([]any)) != 50 {
		t.Fatal("evidence", code, j)
	}
	code, j = get(evidencePath+fmt.Sprintf("&cursor=%.0f", j["next_cursor"]), cookie)
	if code != 200 || len(j["items"].([]any)) != 10 || j["next_cursor"] != float64(0) {
		t.Fatal("evidence paging", code, j)
	}
	code, j = get("/api/usage-analysis/evidence"+period+fmt.Sprintf("&employee_id=%d&session_id=301", hidden.ID), cookie)
	if code != 200 || len(j["items"].([]any)) != 0 {
		t.Fatal("hidden source leak", code, j)
	}
	exec(`update employees set private_work_since=now() where id=$1`, a.ID)
	_, j = get("/api/risk-analysis"+period, cookie)
	if len(j["events"].([]any)) != 0 {
		t.Fatal("privacy change leaked risk", j)
	}
	exec(`update employees set private_work_since=null where id=$1`, a.ID)
	if preview != "" {
		if err := os.WriteFile(preview, []byte(srv.URL+"/__fixture_login"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("fixture preview ready")
		deadline := time.Now().Add(15 * time.Minute)
		for time.Now().Before(deadline) {
			if _, e := os.Stat(preview + ".stop"); e == nil {
				break
			}
			time.Sleep(time.Second)
		}
	}
}
