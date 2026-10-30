package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

func TestReplanSnapshotKeepsOldPlanAndCopiesSnapshot(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "replan_person", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	id := [16]byte{1}
	data := []byte("工作")
	hash := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: hash, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	if err = st.InsertRequest(ctx, store.RequestRow{ID: id, EmployeeID: person.ID, StartedAt: day.Add(time.Hour), Endpoint: "responses", ReqHashes: hash[:]}); err != nil {
		t.Fatal(err)
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.SnapshotNext(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,'{"parts":[]}','queued')`, run); err != nil {
		t.Fatal(err)
	}
	clone, err := q.ReplanSnapshot(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	var superseded bool
	var state string
	var batches, sources, blobs, segments int
	err = st.Pool.QueryRow(ctx, `SELECT r.superseded,r.state,(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1),(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$2 AND NOT planned),(SELECT count(*) FROM wm_full_journal_blobs WHERE run_id=$2),(SELECT count(*) FROM wm_full_journal_segments g JOIN wm_full_journal_sources s ON s.id=g.source_id WHERE s.run_id=$2) FROM wm_full_journal_runs r WHERE id=$1`, run, clone).Scan(&superseded, &state, &batches, &sources, &blobs, &segments)
	if err != nil || !superseded || state != "failed" || batches != 1 || sources != 1 || blobs != 1 || segments != 1 {
		t.Fatalf("archive/copy %v %s %d %d %d %d: %v", superseded, state, batches, sources, blobs, segments, err)
	}
	if _, err = q.ReplanSnapshot(ctx, run); err == nil {
		t.Fatal("superseded run replanned twice")
	}
	if err = q.PlanPaused(ctx, clone, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Step(ctx, run, nil, ""); err == nil {
		t.Fatal("superseded run executable")
	}
}

func TestReplanRejectsEnabledAndAttemptedRuns(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	var run, batch int64
	if err := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','preparing') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReplanSnapshot(ctx, run); err == nil {
		t.Fatal("accepted enabled task")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,'{}','queued') RETURNING id`, run).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_attempts(batch_id) VALUES($1)`, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReplanSnapshot(ctx, run); err == nil {
		t.Fatal("accepted already attempted task")
	}
	var count int
	var superseded bool
	if err := st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wm_full_journal_runs),superseded FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&count, &superseded); err != nil || count != 1 || superseded {
		t.Fatalf("modified on rejection: %d %v %v", count, superseded, err)
	}
}

func TestReplanEvidencePreservesPriorPlanAndSelectsNewFormat(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	var old int64
	if e := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','preparing') RETURNING id`).Scan(&old); e != nil {
		t.Fatal(e)
	}
	if _, e := st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,'{"parts":[]}','queued')`, old); e != nil {
		t.Fatal(e)
	}
	run, e := q.ReplanEvidenceSnapshot(ctx, old)
	if e != nil {
		t.Fatal(e)
	}
	var format string
	var superseded bool
	var count int
	if e = st.Pool.QueryRow(ctx, `SELECT n.plan_format,o.superseded,(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=o.id) FROM wm_full_journal_runs n,wm_full_journal_runs o WHERE n.id=$1 AND o.id=$2`, run, old).Scan(&format, &superseded, &count); e != nil || format != evidenceFormat || !superseded || count != 1 {
		t.Fatalf("new=%s old=%v batches=%d err=%v", format, superseded, count, e)
	}
}
