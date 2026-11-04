package wmjournal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workrecords"
)

func TestPreviewUsesCompletedSessionAndPreservesSnapshot(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	employee, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "preview-member", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	var run, source int64
	var at time.Time
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','running') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,gen_random_uuid(),$2,'员工',650,now(),'responses','ready') RETURNING id,started_at`, run, employee.ID).Scan(&source, &at); err != nil {
		t.Fatal(err)
	}
	session := int64(650)
	b := workrecords.EvidenceBatch{Format: "session-evidence-v1", Parts: []workrecords.EvidencePart{{EmployeeID: employee.ID, SessionID: &session, Occurrences: []workrecords.EvidenceOccurrence{{SourceID: source, Time: at}}}}}
	manifest, _ := json.Marshal(b)
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state,raw_output) VALUES($1,'extract',0,$2,'done','会话证据')`, run, manifest); err != nil {
		t.Fatal(err)
	}
	m := &reductionModel{}
	id, err := GeneratePreview(ctx, st, run, session, m, "mock-sol")
	if err != nil {
		t.Fatal(err)
	}
	var state, output, snapshot string
	if err = st.Pool.QueryRow(ctx, `SELECT state,raw_output,input_snapshot::text FROM wm_log_report_previews WHERE id=$1`, id).Scan(&state, &output, &snapshot); err != nil || state != "done" || !strings.Contains(output, "知识总结") || !strings.Contains(snapshot, "会话证据") {
		t.Fatalf("state=%s output=%q snapshot=%q err=%v", state, output, snapshot, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, employee.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = GeneratePreview(ctx, st, run, session, m, "mock-sol"); err == nil {
		t.Fatal("accepted private source")
	}
}
