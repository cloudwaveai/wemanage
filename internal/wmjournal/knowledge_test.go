package wmjournal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workdigest"
)

type fakeKnowledge struct{ output string }

func (f fakeKnowledge) Extract(context.Context, string, string) (string, string, workdigest.ModelUsage, error) {
	return "", "", workdigest.ModelUsage{}, nil
}
func (f fakeKnowledge) ExtractWithModel(_ context.Context, model, _, _ string) (string, string, workdigest.ModelUsage, error) {
	return f.output, model, workdigest.ModelUsage{}, nil
}
func TestKnowledgeRequiresVerifiableEvidence(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	e, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "knowledge-member", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	var run, source int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,synthesis) VALUES('2026-10-29','done','已整理的知识') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'员工',now(),'responses','ready') RETURNING id`, run, e.ID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	data := []byte("经验依据")
	hash := sha256.Sum256(data)
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',0,$2,'input')`, source, hash[:]); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`{"items":[{"category":"practice","title":"实践","content":"处理方式和条件","evidence":[{"source_id":%d,"direction":"request","ordinal":0,"start":0,"end":6}]}]}`, source)
	if err = ExtractKnowledge(ctx, st, run, fakeKnowledge{raw}, "mock-sol"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_log_knowledge_cards`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count %d %v", count, err)
	}
	if _, err = parseKnowledge(`{"items":[{"category":"skill","title":"x","content":"y","evidence":[]}]}`); err == nil {
		t.Fatal("accepted evidence-free candidate")
	}
	var otherRun int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,synthesis) VALUES('2026-10-30','done','另一日日志') RETURNING id`).Scan(&otherRun); err != nil {
		t.Fatal(err)
	}
	if err = ExtractKnowledge(ctx, st, otherRun, fakeKnowledge{raw}, "mock-sol"); err == nil {
		t.Fatal("accepted evidence from another run")
	}
	var status, stored string
	if err = st.Pool.QueryRow(ctx, `SELECT state,raw_output FROM wm_log_knowledge_runs WHERE journal_run_id=$1`, otherRun).Scan(&status, &stored); err != nil || status != "failed" || stored != raw {
		t.Fatalf("failed attempt not retained: %s %v", status, err)
	}
}
