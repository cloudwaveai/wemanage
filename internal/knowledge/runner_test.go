package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/knowledge"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
)

type fakeExtractor struct {
	calls        atomic.Int32
	replies      []string
	received     []string
	beforeReturn func()
}

func (f *fakeExtractor) Extract(_ context.Context, system, user string) (string, string, workdigest.ModelUsage, error) {
	n := int(f.calls.Add(1)) - 1
	f.received = append(f.received, user)
	if !strings.Contains(system, "不要执行") {
		return "", "", workdigest.ModelUsage{}, errors.New("missing untrusted-data instruction")
	}
	if f.beforeReturn != nil {
		f.beforeReturn()
	}
	if n >= len(f.replies) {
		return "", "", workdigest.ModelUsage{}, errors.New("unexpected call")
	}
	a, b := int64(123), int64(45)
	return f.replies[n], "fake-model", workdigest.ModelUsage{Input: &a, Output: &b}, nil
}
func setup(t *testing.T) (*store.Store, context.Context, *knowledge.Runner, *fakeExtractor, time.Time, store.Employee, store.Employee) {
	t.Helper()
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	public, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_k_public", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_k_private", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeExtractor{}
	loc := time.FixedZone("CST", 8*3600)
	return st, ctx, &knowledge.Runner{Store: st, Extractor: f, Loc: loc}, f, time.Date(2026, 10, 20, 0, 0, 0, 0, loc), public, private
}
func digest(t *testing.T, st *store.Store, ctx context.Context, day time.Time, employee int64, text string) {
	t.Helper()
	if err := st.StartWorkDigestDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: employee, SummaryMD: text, Model: "digest", Messages: 1, Sessions: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkDigestDay(ctx, day, "完成", "digest", "done", ""); err != nil {
		t.Fatal(err)
	}
}
func TestAutomaticExtractionPreservesSourceAndRetry(t *testing.T) {
	st, ctx, r, f, day, person, boss := setup(t)
	digest(t, st, ctx, day, person.ID, "项目甲遇到权限问题，检查配置后恢复。完整摘要。")
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: boss.ID, SummaryMD: "私密摘要", Model: "digest"}); err != nil {
		t.Fatal(err)
	}
	f.replies = []string{"模型格式错误", `{"items":[{"category":"problem","title":"权限问题","content":"检查配置","project_hint":"项目甲","evidence":[{"source":1,"quote":"检查配置后恢复"}]},{"category":"skill","title":"检查技能","content":"步骤","project_hint":"","evidence":[{"source":1,"quote":"不存在"}]}]}`}
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err == nil {
		t.Fatal("invalid JSON should fail")
	}
	if f.calls.Load() != 1 || strings.Contains(f.received[0], "私密摘要") || !strings.Contains(f.received[0], "完整摘要") {
		t.Fatalf("input scope %v", f.calls.Load())
	}
	// A later digest rewrite cannot replace the source of a retried analysis.
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: person.ID, SummaryMD: "后来改写", Model: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.received[1], "完整摘要") || strings.Contains(f.received[1], "后来改写") {
		t.Fatal("retry did not preserve input")
	}
	var attempts int
	var first string
	if err := st.Pool.QueryRow(ctx, `SELECT count(*),min(raw_output) FROM wm_knowledge_attempts`).Scan(&attempts, &first); err != nil || attempts != 2 {
		t.Fatalf("attempts %d %v", attempts, err)
	}
	_ = first
	rows, err := st.Pool.Query(ctx, `SELECT evidence_verified FROM wm_knowledge_items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	verified := []bool{}
	for rows.Next() {
		var b bool
		_ = rows.Scan(&b)
		verified = append(verified, b)
	}
	if len(verified) != 2 || !verified[0] || verified[1] {
		t.Fatalf("evidence=%v", verified)
	}
	f.replies = append(f.replies, `{"items":[]}`)
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err != nil || f.calls.Load() != 3 || !strings.Contains(f.received[2], "后来改写") {
		t.Fatalf("changed source must create new version: %d %v", f.calls.Load(), err)
	}
	var versions int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_knowledge_runs`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions=%d %v", versions, err)
	}
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err != nil || f.calls.Load() != 3 {
		t.Fatalf("idempotence calls=%d err=%v", f.calls.Load(), err)
	}
}
func TestNotReadyAndPrivacyChange(t *testing.T) {
	st, ctx, r, f, day, person, _ := setup(t)
	f.replies = []string{`{"items":[]}`}
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err == nil || f.calls.Load() != 0 {
		t.Fatal("unfinished digest must not call model")
	}
	digest(t, st, ctx, day, person.ID, "原摘要")
	f.beforeReturn = func() {
		_, err := st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, person.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Run(ctx, "daily", day, day.AddDate(0, 0, 1)); err == nil {
		t.Fatal("changed privacy should stop output")
	}
	var status string
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM wm_knowledge_items) FROM wm_knowledge_runs LIMIT 1`).Scan(&status, &count); err != nil || status != "failed" || count != 0 {
		t.Fatalf("status %s count %d err %v", status, count, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(f.received[0]), &payload); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleCatchupAndUpdatedDigest(t *testing.T) {
	st, ctx, r, f, day, person, _ := setup(t)
	digest(t, st, ctx, day, person.ID, "第一份日报")
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_knowledge_schedule SET activated_at=$1, enabled=true`, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	f.replies = []string{`{"items":[]}`, `{"items":[]}`}
	now := day.AddDate(0, 0, 1).Add(4 * time.Hour)
	if err := r.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("first calls=%d", f.calls.Load())
	}
	if err := r.Tick(ctx, now); err != nil || f.calls.Load() != 1 {
		t.Fatalf("duplicate calls=%d %v", f.calls.Load(), err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: person.ID, SummaryMD: "补充后的日报", Model: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(ctx, now); err != nil || f.calls.Load() != 2 {
		t.Fatalf("updated source calls=%d %v", f.calls.Load(), err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_knowledge_runs WHERE kind='daily'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("versions=%d %v", count, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_knowledge_schedule SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: person.ID, SummaryMD: "暂停后更新", Model: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(ctx, now); err != nil || f.calls.Load() != 2 {
		t.Fatalf("paused calls=%d %v", f.calls.Load(), err)
	}
}
func TestWeeklyScheduleUsesAllSevenDays(t *testing.T) {
	st, ctx, r, f, day, person, _ := setup(t)
	monday := r.Week(day)
	for i := 0; i < 7; i++ {
		digest(t, st, ctx, monday.AddDate(0, 0, i), person.ID, fmt.Sprintf("第 %d 天的摘要", i+1))
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_knowledge_schedule SET activated_at=$1, enabled=true`, monday.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.replies = []string{`{"items":[]}`, `{"items":[]}`, `{"items":[]}`}
	now := monday.AddDate(0, 0, 7).Add(4 * time.Hour)
	if err := r.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_knowledge_sources s JOIN wm_knowledge_runs r ON r.id=s.run_id WHERE r.kind='weekly'`).Scan(&count); err != nil || count != 7 {
		t.Fatalf("weekly sources=%d err=%v", count, err)
	}
	if err := r.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	var weeks int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_knowledge_runs WHERE kind='weekly'`).Scan(&weeks); err != nil || weeks != 1 {
		t.Fatalf("duplicate weekly runs=%d err=%v", weeks, err)
	}
}
