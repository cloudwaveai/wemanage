package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/wmfocus"
	"aiseat/internal/workdigest"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type focusModel struct{ input string }

func (m *focusModel) Extract(_ context.Context, _, _ string) (string, string, workdigest.ModelUsage, error) {
	return "", "", workdigest.ModelUsage{}, nil
}
func (m *focusModel) ExtractWithModel(_ context.Context, model, _, input string) (string, string, workdigest.ModelUsage, error) {
	m.input = input
	return "项目问题与解决思路", model, workdigest.ModelUsage{}, nil
}
func TestFocusSearchAndRawAnalysisPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if e := wemanage.Install(ctx, st); e != nil {
		t.Fatal(e)
	}
	if e := st.EnsureSuperAdmin(ctx, "focus-admin", "管理员"); e != nil {
		t.Fatal(e)
	}
	admin, e := st.GetEmployeeByOpenID(ctx, "focus-admin")
	if e != nil {
		t.Fatal(e)
	}
	emp, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "focus-person", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	raw := []byte(`{"role":"user","content":"项目A 原始完整内容"}`)
	h := sha256.Sum256(raw)
	if e = st.PutBlobs(ctx, []store.Blob{{Hash: h, Kind: "input", Data: raw}}, now); e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	var reqid [16]byte
	copy(reqid[:], id[:])
	sess := int64(42)
	if e = st.InsertRequest(ctx, store.RequestRow{ID: reqid, StartedAt: now, EmployeeID: emp.ID, SessionID: &sess, ClientKind: "codex", Endpoint: "responses", StatusCode: 200, ReqHashes: h[:]}); e != nil {
		t.Fatal(e)
	}
	if e = st.InsertTexts(ctx, []store.TextRow{{RequestID: reqid, EmployeeID: emp.ID, SessionID: &sess, StartedAt: now, Role: "user", Text: "项目A 索引"}}); e != nil {
		t.Fatal(e)
	}
	var run, item int64
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status) values('daily','2026-11-01','2026-11-02','done') returning id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) values($1,1,'2026-11-01',$2,'成员','摘要','model',now())`, run, emp.ID); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `insert into wm_knowledge_items(run_id,category,title,content,evidence) values($1,'project','项目A','线索','[{"source":1,"quote":"摘要"}]') returning id`, run).Scan(&item); e != nil {
		t.Fatal(e)
	}
	cookie, csrf, e := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	call := func(method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		r.Header.Set("X-CSRF-Token", csrf)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := call("GET", fmt.Sprintf("/api/focus/%d/search?q=%%E9%%A1%%B9%%E7%%9B%%AEA", item), ""); code != 200 || !strings.Contains(b, id.String()) {
		t.Fatalf("search %d %s", code, b)
	}
	data, _ := json.Marshal(map[string]string{"keyword": "项目A", "request": id.String()})
	if code, b := call("POST", fmt.Sprintf("/api/focus/%d", item), string(data)); code != 200 {
		t.Fatalf("queue %d %s", code, b)
	}
	m := &focusModel{}
	worked, e := wmfocus.ProcessOne(ctx, st, m, "sol")
	if e != nil || !worked || !strings.Contains(m.input, "原始完整内容") || strings.Contains(m.input, "项目A 索引") {
		t.Fatalf("worker=%v err=%v input=%q", worked, e, m.input)
	}
	if code, b := call("GET", "/api/focus", ""); code != 200 || !strings.Contains(b, "项目问题与解决思路") {
		t.Fatalf("result %d %s", code, b)
	}
	// A large window is split and every byte of the stored input reaches a segment.
	long := []byte(`{"role":"user","content":"` + strings.Repeat("详细内容", 18000) + `"}`)
	longHash := sha256.Sum256(long)
	if e = st.PutBlobs(ctx, []store.Blob{{Hash: longHash, Kind: "input", Data: long}}, now); e != nil {
		t.Fatal(e)
	}
	id2 := uuid.New()
	var req2 [16]byte
	copy(req2[:], id2[:])
	later := now.Add(time.Hour)
	if e = st.InsertRequest(ctx, store.RequestRow{ID: req2, StartedAt: later, EmployeeID: emp.ID, SessionID: &sess, ClientKind: "codex", Endpoint: "responses", StatusCode: 200, ReqHashes: longHash[:]}); e != nil {
		t.Fatal(e)
	}
	if e = st.InsertTexts(ctx, []store.TextRow{{RequestID: req2, EmployeeID: emp.ID, SessionID: &sess, StartedAt: later, Role: "user", Text: "项目A 索引"}}); e != nil {
		t.Fatal(e)
	}
	data, _ = json.Marshal(map[string]string{"keyword": "项目A", "request": id2.String()})
	if code, b := call("POST", fmt.Sprintf("/api/focus/%d", item), string(data)); code != 200 {
		t.Fatalf("large queue %d %s", code, b)
	}
	if worked, e = wmfocus.ProcessOne(ctx, st, m, "sol"); e != nil || !worked {
		t.Fatalf("large processing %v %v", worked, e)
	}
	var same bool
	var chunks int
	if e = st.Pool.QueryRow(ctx, `select f.input_snapshot=(select string_agg(c.input,'' order by c.ordinal) from wm_focus_chunks c where c.job_id=f.id and c.phase='extract'),(select count(*) from wm_focus_chunks c where c.job_id=f.id and c.phase='extract') from wm_focus_jobs f where f.request_id=$1`, id2).Scan(&same, &chunks); e != nil || !same || chunks < 2 {
		t.Fatalf("complete=%v chunks=%d err=%v", same, chunks, e)
	}
	if _, e = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, emp.ID); e != nil {
		t.Fatal(e)
	}
	if code, b := call("GET", "/api/focus", ""); code != 200 || strings.Contains(b, "项目问题与解决思路") {
		t.Fatalf("privacy %d %s", code, b)
	}
	if code, b := call("GET", fmt.Sprintf("/api/focus/%d/search?q=%%E9%%A1%%B9%%E7%%9B%%AEA", item), ""); code != 404 {
		t.Fatalf("private search %d %s", code, b)
	}
}
