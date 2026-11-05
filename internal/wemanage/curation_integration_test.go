package wemanage_test

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workdigest"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type curationMock struct{ calls *atomic.Int32 }

func (m curationMock) ExtractWithModel(_ context.Context, model, prompt, material string) (string, string, workdigest.ModelUsage, error) {
	m.calls.Add(1)
	if model != "gpt-6-sol" {
		return "", "", workdigest.ModelUsage{}, fmt.Errorf("wrong configured model: %s", model)
	}
	var projects []struct {
		Name  string `json:"name"`
		Clues []struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
		} `json:"clues"`
	}
	if err := json.Unmarshal([]byte(material), &projects); err != nil {
		return "", "", workdigest.ModelUsage{}, err
	}
	if strings.Contains(material, "私密") {
		return "", "", workdigest.ModelUsage{}, fmt.Errorf("private data in model input")
	}
	a, b := projects[0], projects[1]
	output := map[string]any{"merges": []any{map[string]any{"name": b.Name, "target": a.Name, "reason": "同一项目的两种名称", "evidence": []any{map[string]any{"item_id": a.Clues[0].ID, "quote": a.Clues[0].Content}, map[string]any{"item_id": b.Clues[0].ID, "quote": b.Clues[0].Content}}}}, "items": []any{map[string]any{"source_item_id": a.Clues[0].ID, "title": "日志核查方法", "kind": "skill", "disposition": "new", "reason": "两次记录相同方法", "related_item_ids": []int64{b.Clues[0].ID}, "content": "## 何时使用\n发布后\n## 操作步骤\n保存日志并核查\n## 如何验证\n检查记录完整", "flow": []any{map[string]any{"id": "S1", "text": "保存日志", "next": []any{map[string]string{"condition": "已保存", "target": "S2"}}}, map[string]any{"id": "S2", "text": "检查日志完整；缺失则人工确认", "next": []any{}}}, "evidence": []any{map[string]any{"item_id": a.Clues[0].ID, "quote": a.Clues[0].Content}, map[string]any{"item_id": b.Clues[0].ID, "quote": b.Clues[0].Content}}}}, "gaps": []string{"尚无失败分支的已验证操作"}}
	raw, _ := json.Marshal(output)
	return string(raw), model, workdigest.ModelUsage{}, nil
}
func TestCurationPipelineDraftReuseAndPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `update wm_full_journal_settings set sol_model='gpt-6-sol' where id=1`); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "cur-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.GetEmployeeByOpenID(ctx, "cur-admin")
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "cur-member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "cur-boss", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	for index, emp := range []store.Employee{member, boss} {
		var run int64
		if err = st.Pool.QueryRow(ctx, `insert into wm_knowledge_runs(kind,period_start,period_end,status,version)values('daily','2026-11-01','2026-11-01','done',$1)returning id`, index+1).Scan(&run); err != nil {
			t.Fatal(err)
		}
		_, err = st.Pool.Exec(ctx, `insert into wm_knowledge_sources(run_id,ordinal,source_day,employee_id,employee_name,summary_md,digest_model,digest_generated_at)values($1,1,'2026-11-01',$2,$3,'保存日志并检查结果','test',now())`, run, emp.ID, emp.Name)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"甲项目", "项目甲"}
		if index == 1 {
			names = []string{"私密项目"}
		}
		for _, name := range names {
			_, err = st.Pool.Exec(ctx, `insert into wm_knowledge_items(run_id,category,title,content,project_hint,evidence)values($1,'skill','核查','保存日志并检查结果',$2,'[{"source":1,"quote":"保存日志并检查结果"}]')`, run, name)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	cookie, csrf, _ := st.CreateWebSession(ctx, admin.ID, time.Hour)
	memberCookie, _, _ := st.CreateWebSession(ctx, member.ID, time.Hour)
	calls := &atomic.Int32{}
	srv := httptest.NewServer((&wemanage.Service{Store: st, Summarizer: curationMock{calls}}).Handler())
	defer srv.Close()
	call := func(method, path, body, auth string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: auth})
		req.Header.Set("X-CSRF-Token", csrf)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := call("POST", "/api/curation", "{}", cookie); code != 202 {
		t.Fatal(code, b)
	}
	var result struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	var body string
	for n := 0; n < 150; n++ {
		_, body = call("GET", "/api/curation", "", cookie)
		_ = json.Unmarshal([]byte(body), &result)
		if result.State == "done" || result.State == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if result.State != "done" || !strings.Contains(body, "日志核查方法") {
		t.Fatal(body)
	}
	if code, b := call("POST", "/api/curation", "{}", cookie); code != 200 || !strings.Contains(b, `"reused":true`) {
		t.Fatal(code, b)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate model call", calls.Load())
	}
	if _, err = st.Pool.Exec(ctx, `update wm_curation_runs set state='failed',error='validator retry' where id=$1`, result.ID); err != nil {
		t.Fatal(err)
	}
	if err = (&wemanage.Service{Store: st}).RevalidateCuration(ctx, result.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("revalidation called model")
	}
	if code, _ := call("GET", "/api/curation", "", memberCookie); code != 403 {
		t.Fatal("member can access curation", code)
	}
	for i := 0; i < 2; i++ {
		if code, b := call("POST", fmt.Sprintf("/api/curation/%d/actions", result.ID), `{"index":0,"action":"draft"}`, cookie); code != 200 {
			t.Fatal(code, b)
		}
	}
	_, body = call("GET", "/api/assets", "", cookie)
	if !strings.Contains(body, "skill-flow") || !strings.Contains(body, `"state":"draft"`) {
		t.Fatal(body)
	}
	var count int
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_asset_versions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("not idempotent", count, err)
	}
	_, body = call("GET", "/api/shared/assets", "", memberCookie)
	if strings.Contains(body, "日志核查方法") {
		t.Fatal("draft published")
	}
	// Daily scheduler reuses the completed batch and survives repeated ticks.
	if code, b := call("PUT", "/api/curation/schedule", `{"enabled":true,"hour":9,"minute":0}`, cookie); code != 200 {
		t.Fatal(code, b)
	}
	if code, b := call("PUT", "/api/curation/schedule", `{"enabled":true,"hour":25,"minute":0}`, cookie); code != 400 {
		t.Fatal(code, b)
	}
	if code, _ := call("GET", "/api/curation/schedule", "", memberCookie); code != 403 {
		t.Fatal("member accessed schedule", code)
	}
	scheduled := &wemanage.Service{Store: st, Summarizer: curationMock{calls}}
	now := time.Date(2026, 11, 7, 0, 59, 0, 0, time.UTC) // 08:59 Asia/Shanghai
	if err = scheduled.CurationScheduleTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_curation_schedule_attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ran before configured time", count, err)
	}
	now = now.Add(time.Minute)
	if _, err = st.Pool.Exec(ctx, `update wm_knowledge_runs set status='running'`); err != nil {
		t.Fatal(err)
	}
	if err = scheduled.CurationScheduleTick(ctx, now); err != nil {
		t.Fatal(err)
	}
	var waiting string
	if err = st.Pool.QueryRow(ctx, `select state from wm_curation_schedules where owner_id=$1`, admin.ID).Scan(&waiting); err != nil || waiting != "waiting" {
		t.Fatal("did not wait for sources", waiting, err)
	}
	if _, err = st.Pool.Exec(ctx, `update wm_knowledge_runs set status='done'`); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err = scheduled.CurationScheduleTick(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	var scheduleState string
	for n := 0; n < 100; n++ {
		if err = st.Pool.QueryRow(ctx, `select state from wm_curation_schedules where owner_id=$1`, admin.ID).Scan(&scheduleState); err != nil {
			t.Fatal(err)
		}
		if scheduleState != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if scheduleState != "reused" || calls.Load() != 1 {
		t.Fatal("schedule did not reuse", scheduleState, calls.Load())
	}
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_curation_schedule_attempts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate daily attempt", count, err)
	}
	if code, b := call("GET", fmt.Sprintf("/api/curation?id=%d", result.ID), "", cookie); code != 200 || !strings.Contains(b, "日志核查方法") {
		t.Fatal("historical result lost", code, b)
	}
	if code, b := call("GET", "/api/curation/history", "", cookie); code != 200 || !strings.Contains(b, `"runs":[`) {
		t.Fatal(code, b)
	}
	if code, b := call("PUT", "/api/curation/schedule", `{"enabled":false,"hour":9,"minute":0}`, cookie); code != 200 {
		t.Fatal(code, b)
	}
	if err = scheduled.CurationScheduleTick(ctx, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `select count(*) from wm_curation_schedule_attempts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("disabled schedule ran", count, err)
	}
	// Changes to any model input must hide both the output and its derived asset.
	_, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, body = call("GET", "/api/curation", "", cookie)
	if !strings.Contains(body, `"state":"outdated"`) || strings.Contains(body, "日志核查方法") {
		t.Fatal("private analysis exposed", body)
	}
	_, body = call("GET", "/api/assets", "", cookie)
	if strings.Contains(body, "日志核查方法") {
		t.Fatal("private derived asset exposed", body)
	}
	if code, b := call("POST", fmt.Sprintf("/api/curation/%d/actions", result.ID), `{"index":0,"action":"draft"}`, cookie); code != 409 {
		t.Fatal("private action allowed", code, b)
	}
}
