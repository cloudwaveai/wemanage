package wemanage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestLogKnowledgePrivacyAndReview(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "knowledge-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := st.GetEmployeeByOpenID(ctx, "knowledge-admin")
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "knowledge-boss", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	var publicRun, privateRun, publicJob, privateJob, publicCard, privateCard int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','done') RETURNING id`).Scan(&publicRun); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-30','done') RETURNING id`).Scan(&privateRun); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'林伊默',now(),'responses','ready')`, privateRun, boss.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_knowledge_runs(journal_run_id,state,raw_output) VALUES($1,'done','公开原稿') RETURNING id`, publicRun).Scan(&publicJob); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_knowledge_runs(journal_run_id,state,raw_output) VALUES($1,'done','私密原稿') RETURNING id`, privateRun).Scan(&privateJob); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_knowledge_cards(knowledge_run_id,category,title,content,evidence) VALUES($1,'practice','可见经验','内容','[]') RETURNING id`, publicJob).Scan(&publicCard); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_knowledge_cards(knowledge_run_id,category,title,content,evidence) VALUES($1,'practice','私密经验','内容','[]') RETURNING id`, privateJob).Scan(&privateCard); err != nil {
		t.Fatal(err)
	}
	cookie, csrf, err := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer server.Close()
	call := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		req.Header.Set("X-CSRF-Token", csrf)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	if code, body := call("GET", "/api/log-knowledge", ""); code != 200 || !strings.Contains(body, "可见经验") || strings.Contains(body, "私密经验") {
		t.Fatalf("privacy %d %s", code, body)
	}
	if code, body := call("GET", "/api/log-knowledge/runs", ""); code != 200 || !strings.Contains(body, "公开原稿") || strings.Contains(body, "私密原稿") {
		t.Fatalf("run privacy %d %s", code, body)
	}
	var visibleReview, hiddenReview int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_period_runs(kind,period_start,period_end,coverage_start,state,report) VALUES('monthly','2026-09-01','2026-10-01','2026-09-01','done','公开复盘') RETURNING id`).Scan(&visibleReview); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_period_runs(kind,period_start,period_end,coverage_start,state,report) VALUES('monthly','2026-10-01','2026-11-01','2026-10-01','done','私密复盘') RETURNING id`).Scan(&hiddenReview); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_log_period_sources(period_run_id,journal_run_id) VALUES($1,$2),($3,$4)`, visibleReview, publicRun, hiddenReview, privateRun); err != nil {
		t.Fatal(err)
	}
	if code, body := call("GET", "/api/log-reviews", ""); code != 200 || !strings.Contains(body, "公开复盘") || strings.Contains(body, "私密复盘") {
		t.Fatalf("review privacy %d %s", code, body)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_log_report_previews(journal_run_id,session_id,state,source_batch_ids,input_snapshot,raw_output) VALUES($1,1,'done','{}','{}','公开预览'),($2,2,'done','{}','{}','私密预览')`, publicRun, privateRun); err != nil {
		t.Fatal(err)
	}
	if code, body := call("GET", "/api/log-previews", ""); code != 200 || !strings.Contains(body, "公开预览") || strings.Contains(body, "私密预览") {
		t.Fatalf("preview privacy %d %s", code, body)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_compact_jobs(run_id,employee_id,session_id,phase,ordinal,state,input,output) VALUES($1,0,0,'daily',0,'done','', '公开日报'),($2,0,0,'daily',0,'done','', '私密日报')`, publicRun, privateRun); err != nil {
		t.Fatal(err)
	}
	if code, body := call("GET", "/api/compact-reports", ""); code != 200 || !strings.Contains(body, "公开日报") || strings.Contains(body, "私密日报") {
		t.Fatalf("compact report privacy %d %s", code, body)
	}
	var publicSkill, privateSkill, publicDigestRun, privateDigestRun int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_knowledge_runs(kind,period_start,period_end,status) VALUES('daily','2026-10-29','2026-10-30','done') RETURNING id`).Scan(&publicDigestRun); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_knowledge_runs(kind,period_start,period_end,status) VALUES('daily','2026-10-30','2026-10-31','done') RETURNING id`).Scan(&privateDigestRun); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at) VALUES($1,1,'2026-10-30',$2,'林伊默','私密','model',now())`, privateDigestRun, boss.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_knowledge_items(run_id,category,title,content,evidence) VALUES($1,'skill','公开 Skill','内容','[]') RETURNING id`, publicDigestRun).Scan(&publicSkill); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_knowledge_items(run_id,category,title,content,evidence) VALUES($1,'skill','私密 Skill','内容','[]') RETURNING id`, privateDigestRun).Scan(&privateSkill); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_skill_drafts(source_item_id,variant,title,trigger_text,steps,verification,limits_text,skill_md) VALUES($1,'x','公开图解','条件','["步骤"]','核对','边界','原文'),($2,'x','私密图解','条件','["步骤"]','核对','边界','原文')`, publicSkill, privateSkill); err != nil {
		t.Fatal(err)
	}
	if code, body := call("GET", "/api/skill-drafts", ""); code != 200 || !strings.Contains(body, "公开图解") || strings.Contains(body, "私密图解") {
		t.Fatalf("skill privacy %d %s", code, body)
	}
	var privateDraft, publicDraft int64
	if err = st.Pool.QueryRow(ctx, `SELECT (SELECT id FROM wm_skill_drafts WHERE source_item_id=$1),(SELECT id FROM wm_skill_drafts WHERE source_item_id=$2)`, privateSkill, publicSkill).Scan(&privateDraft, &publicDraft); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("PUT", fmt.Sprintf("/api/skill-drafts/%d", privateDraft), `{"status":"confirmed"}`); code != 404 {
		t.Fatalf("private skill review %d", code)
	}
	if code, _ := call("PUT", fmt.Sprintf("/api/skill-drafts/%d", publicDraft), `{"status":"confirmed"}`); code != 200 {
		t.Fatalf("public skill review %d", code)
	}
	if code, _ := call("PUT", fmt.Sprintf("/api/log-knowledge/%d", privateCard), `{"status":"confirmed"}`); code != 404 {
		t.Fatalf("private mutation %d", code)
	}
	if code, _ := call("PUT", fmt.Sprintf("/api/log-knowledge/%d", publicCard), `{"status":"confirmed"}`); code != 200 {
		t.Fatalf("public mutation %d", code)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_log_knowledge_runs SET state='failed',raw_output='失败原稿' WHERE id IN ($1,$2)`, publicJob, privateJob); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_log_knowledge_attempts(knowledge_run_id,raw_output) VALUES($1,'失败原稿')`, publicJob); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("POST", fmt.Sprintf("/api/log-knowledge/runs/%d/retry", privateJob), `{"accept_unknown":true}`); code != 404 {
		t.Fatalf("private retry %d", code)
	}
	if code, _ := call("POST", fmt.Sprintf("/api/log-knowledge/runs/%d/retry", publicJob), `{"accept_unknown":true}`); code != 200 {
		t.Fatalf("public retry %d", code)
	}
	var state, prior string
	if err = st.Pool.QueryRow(ctx, `SELECT k.state,a.raw_output FROM wm_log_knowledge_runs k JOIN wm_log_knowledge_attempts a ON a.knowledge_run_id=k.id WHERE k.id=$1`, publicJob).Scan(&state, &prior); err != nil || state != "queued" || prior != "失败原稿" {
		t.Fatalf("knowledge retry lost output %s %q %v", state, prior, err)
	}
	var batch int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_log_period_batches(period_run_id,level,ordinal,is_final,input_manifest,state,raw_output) VALUES($1,0,0,true,'{}','failed','旧复盘原稿') RETURNING id`, visibleReview).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_log_period_attempts(batch_id,raw_output) VALUES($1,'旧复盘原稿')`, batch); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("POST", fmt.Sprintf("/api/log-reviews/%d/retry", hiddenReview), `{"accept_unknown":true}`); code != 404 {
		t.Fatalf("private period retry %d", code)
	}
	if code, _ := call("POST", fmt.Sprintf("/api/log-reviews/%d/retry", visibleReview), `{"accept_unknown":true}`); code != 200 {
		t.Fatalf("period retry %d", code)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT b.state,a.raw_output FROM wm_log_period_batches b JOIN wm_log_period_attempts a ON a.batch_id=b.id WHERE b.id=$1`, batch).Scan(&state, &prior); err != nil || state != "queued" || prior != "旧复盘原稿" {
		t.Fatalf("period retry lost output %s %q %v", state, prior, err)
	}
}
