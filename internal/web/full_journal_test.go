package web_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"
	"github.com/go-chi/chi/v5"
)

func TestFullJournalAccessFollowsSourcePrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	set := settings.NewService(st)
	if err := st.EnsureSuperAdmin(ctx, "ou_full_admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_person", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	var run, source int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,daily_report,synthesis) VALUES('2026-10-29','done','日报正文','总结正文') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'成员',now(),'responses','ready') RETURNING id`, run, person.ID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_runs(day,state,daily_report,previous_run_id) VALUES('2026-10-30','done','延续前日日报',$1)`, run); err != nil {
		t.Fatal(err)
	}
	srv := web.NewServer(web.Deps{Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true})
	router := chi.NewRouter()
	srv.Mount(router)
	ts := httptest.NewServer(router)
	defer ts.Close()
	admin := login(t, ts, "ou_full_admin")
	employee := login(t, ts, "ou_full_person")
	if resp, _ := employee.do("GET", "/api/admin/full-journal", ""); resp.StatusCode != 403 {
		t.Fatalf("ordinary %d", resp.StatusCode)
	}
	resp, body := admin.do("GET", "/api/admin/full-journal", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "日报正文") {
		t.Fatalf("admin %d %s", resp.StatusCode, body)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET state='failed' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state,error) VALUES($1,'extract',0,'{}','failed','模型调用失败')`, run); err != nil {
		t.Fatal(err)
	}
	resp, _ = admin.do("POST", fmt.Sprintf("/api/admin/full-journal/%d/retry", run), `{"accept_unknown":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("retry %d", resp.StatusCode)
	}
	var batchState string
	if err = st.Pool.QueryRow(ctx, `SELECT state FROM wm_full_journal_batches WHERE run_id=$1`, run).Scan(&batchState); err != nil || batchState != "queued" {
		t.Fatalf("retry state=%s %v", batchState, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET state='failed',superseded=true WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do("GET", "/api/admin/full-journal", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"state":"superseded"`) {
		t.Fatalf("archive status %d %s", resp.StatusCode, body)
	}
	resp, _ = admin.do("POST", fmt.Sprintf("/api/admin/full-journal/%d/retry", run), `{"accept_unknown":true}`)
	if resp.StatusCode != 404 {
		t.Fatalf("archived retry %d", resp.StatusCode)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, person.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do("GET", "/api/admin/full-journal", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "日报正文") || strings.Contains(string(body), "总结正文") || strings.Contains(string(body), "延续前日日报") {
		t.Fatalf("privacy %d %s", resp.StatusCode, body)
	}
}
