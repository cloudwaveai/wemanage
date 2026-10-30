package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestEstimateIsReadOnlyAndRespectsSessionBoundaries(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	a, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "estimate-a", Name: "成员A"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "estimate-b", Name: "成员B"})
	if e != nil {
		t.Fatal(e)
	}
	var run int64
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','preparing') RETURNING id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	data := []byte("完整上下文")
	hash := sha256.Sum256(data)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); e != nil {
		t.Fatal(e)
	}
	for i, employee := range []int64{a.ID, a.ID, b.ID, a.ID} {
		var source int64
		var session any = int64(7)
		if i == 3 {
			session = nil
		}
		uuid := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000004"}[i]
		e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,$2,$3,'成员',$4,now(),'responses','ready') RETURNING id`, run, uuid, employee, session).Scan(&source)
		if e != nil {
			t.Fatal(e)
		}
		for j := 0; j < 2; j++ {
			if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',$2,$3,'input')`, source, j, hash[:]); e != nil {
				t.Fatal(e)
			}
		}
	}
	estimate, e := q.EstimateEvidence(ctx, run, nil)
	if e != nil {
		t.Fatal(e)
	}
	if estimate.Groups != 3 || estimate.Occurrences != 8 || estimate.Batches != 1 || estimate.ManifestBytes == 0 || estimate.ModelPayloadBytes <= estimate.ManifestBytes {
		t.Fatalf("estimate %+v", estimate)
	}
	var writes int
	if e = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1`, run).Scan(&writes); e != nil || writes != 0 {
		t.Fatalf("wrote %d: %v", writes, e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO work_digest_days(day,status) VALUES('2026-10-29','done')`); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO work_digests(day,employee_id,summary_md) VALUES('2026-10-29',$1,'原日报线索')`, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET plan_format=$2 WHERE id=$1`, run, evidenceFormat); e != nil {
		t.Fatal(e)
	}
	if e = q.PlanEvidenceRun(ctx, run, true, nil); !errors.Is(e, errEvidenceInterrupted) {
		t.Fatalf("ignored scheduler setting: %v", e)
	}
	if e = q.PlanEvidenceRun(ctx, run, false, nil); e != nil {
		t.Fatal(e)
	}
	var state string
	var planned, batches int
	if e = st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND planned),(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state, &planned, &batches); e != nil || state != "queued" || planned != 4 || batches != 1 {
		t.Fatalf("plan %s %d %d %v", state, planned, batches, e)
	}
	model := &evidenceCapture{}
	if ok, err := q.ProcessNext(ctx, run, model); err != nil || !ok || model.calls != 1 {
		t.Fatalf("model %v %v %d", ok, err, model.calls)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE work_digests SET summary_md='后来改动' WHERE employee_id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = q.PrepareReduction(ctx, run, "extract", 0, 100000); e != nil {
		t.Fatal(e)
	}
	var reduction string
	if e = st.Pool.QueryRow(ctx, `SELECT input_manifest::text FROM wm_full_journal_batches WHERE run_id=$1 AND phase='daily'`, run).Scan(&reduction); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(reduction, "原日报线索") || strings.Contains(reduction, "后来改动") || !strings.Contains(reduction, "gateway_daily_context") {
		t.Fatal("daily context not snapshotted and labelled")
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = q.EstimateEvidence(ctx, run, nil); e == nil {
		t.Fatal("accepted private source")
	}
}
