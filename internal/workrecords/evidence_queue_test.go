package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestEvidencePlanningRollsBackOnMissingSnapshot(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	emp, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "evidence-rollback", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	var run, source int64
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,plan_format) VALUES('2026-10-29','preparing',$1) RETURNING id`, evidenceFormat).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'成员',1,now(),'responses','ready') RETURNING id`, run, emp.ID).Scan(&source); e != nil {
		t.Fatal(e)
	}
	data := []byte(strings.Repeat("a", 300000))
	hash := sha256.Sum256(data)
	missing := sha256.Sum256([]byte("missing"))
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',0,$2,'input'),($1,'response',0,$3,'output')`, source, hash[:], missing[:]); e != nil {
		t.Fatal(e)
	}
	if e = q.PlanEvidenceRun(ctx, run, false, nil); e == nil {
		t.Fatal("accepted missing blob")
	}
	var state string
	var count int
	if e = st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state, &count); e != nil || state != "preparing" || count != 0 {
		t.Fatalf("partially published %s %d %v", state, count, e)
	}
}
