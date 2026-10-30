package workrecords

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
)

type fakeExtraction struct{ calls int }

func (f *fakeExtraction) Extract(_ context.Context, _, _ string) (string, string, workdigest.ModelUsage, error) {
	f.calls++
	return "已记录的工作过程", "mock", workdigest.ModelUsage{}, nil
}
func (f *fakeExtraction) ExtractWithModel(ctx context.Context, model, system, material string) (string, string, workdigest.ModelUsage, error) {
	f.calls++
	return "Sol 综合结果", model, workdigest.ModelUsage{}, nil
}

func TestQueueSnapshotsAllSourcesAndPreservesPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_public", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_private", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	data := []byte(`{"content":"完整工作内容"}`)
	hash := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: hash, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	for i, emp := range []store.Employee{person, boss} {
		id := [16]byte{byte(i + 1)}
		if err = st.InsertRequest(ctx, store.RequestRow{ID: id, EmployeeID: emp.ID, StartedAt: day.Add(time.Hour), Endpoint: "responses", ReqHashes: hash[:]}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	more, err := q.SnapshotNext(ctx, run)
	if err != nil || !more {
		t.Fatalf("snapshot %v %v", more, err)
	}
	more, err = q.SnapshotNext(ctx, run)
	if err != nil || more {
		t.Fatalf("end %v %v", more, err)
	}
	var count, missing, segments int
	var got []byte
	err = st.Pool.QueryRow(ctx, `SELECT source_count,missing_count,(SELECT count(*) FROM wm_full_journal_segments s JOIN wm_full_journal_sources a ON a.id=s.source_id WHERE a.run_id=$1),(SELECT data FROM wm_full_journal_blobs WHERE run_id=$1 LIMIT 1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&count, &missing, &segments, &got)
	if err != nil || count != 1 || missing != 0 || segments != 1 || string(got) != string(data) {
		t.Fatalf("snapshot count=%d missing=%d segments=%d err=%v", count, missing, segments, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET plan_format='legacy' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if err = q.PlanRun(ctx, run, 4096); err != nil {
		t.Fatal(err)
	}
	var batches int
	var state string
	if err = st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state, &batches); err != nil || state != "queued" || batches != 1 {
		t.Fatalf("plan %s %d %v", state, batches, err)
	}
	model := &fakeExtraction{}
	more, err = q.ProcessNext(ctx, run, model)
	if err != nil || !more || model.calls != 1 {
		t.Fatalf("process %v %d %v", more, model.calls, err)
	}
	more, err = q.ProcessNext(ctx, run, model)
	if err != nil || more || model.calls != 1 {
		t.Fatalf("duplicate %v %d %v", more, model.calls, err)
	}
	var attempts int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=$1`, run).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempts %d %v", attempts, err)
	}
	phase, level, err := q.PrepareReduction(ctx, run, "extract", 0, 4096)
	if err != nil || phase != "daily" {
		t.Fatalf("reduction %s %d %v", phase, level, err)
	}
	if _, err = q.ProcessReduction(ctx, run, phase, level, model, "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareSynthesis(ctx, run, 4096); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ProcessReduction(ctx, run, "synthesis", 0, model, "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if err = q.Finish(ctx, run); err != nil {
		t.Fatal(err)
	}
	var report, synthesis string
	if err = st.Pool.QueryRow(ctx, `SELECT state,daily_report,synthesis FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state, &report, &synthesis); err != nil || state != "done" || report == "" || synthesis != "Sol 综合结果" {
		t.Fatalf("finish %s %v", state, err)
	}
}

func TestQueueMarksMissingAndStopsWhenPrivacyChanges(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_gap", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	id := [16]byte{8}
	missing := [32]byte{9}
	if err = st.InsertRequest(ctx, store.RequestRow{ID: id, EmployeeID: person.ID, StartedAt: day.Add(time.Hour), Endpoint: "responses", ReqHashes: missing[:]}); err != nil {
		t.Fatal(err)
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.SnapshotNext(ctx, run); err != nil {
		t.Fatal(err)
	}
	var count int
	var state string
	if err = st.Pool.QueryRow(ctx, `SELECT missing_count,(SELECT status FROM wm_full_journal_sources WHERE run_id=$1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&count, &state); err != nil || count != 1 || state != "missing" {
		t.Fatalf("gap %d %s %v", count, state, err)
	}
	run, err = q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, person.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.SnapshotNext(ctx, run); err == nil {
		t.Fatal("private source must stop snapshot")
	}
}

func TestScheduledFullJournalRunsAllPhases(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	f := &fakeExtraction{}
	loc := time.FixedZone("CST", 8*3600)
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, loc)
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_scheduler", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"content":"目标和结果"}`)
	hash := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: hash, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{15}, EmployeeID: person.ID, StartedAt: day.Add(time.Hour), Endpoint: "responses", ReqHashes: hash[:]}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true,activated_at=$1 WHERE id=1`, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = q.Tick(ctx, f, day.AddDate(0, 0, 1).Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = st.Pool.QueryRow(ctx, `SELECT state FROM wm_full_journal_runs WHERE day=$1::date`, day).Scan(&state); err != nil || state != "done" || f.calls != 3 {
		t.Fatalf("state=%s calls=%d err=%v", state, f.calls, err)
	}
	if err = q.Tick(ctx, f, day.AddDate(0, 0, 1).Add(4*time.Hour)); err != nil || f.calls != 3 {
		t.Fatalf("duplicate calls=%d err=%v", f.calls, err)
	}
}

func TestRepeatedHistoryUsesSingleSnapshotBlob(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_full_repeat", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"content":"上下文"}`)
	hash := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: hash, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	session := int64(5)
	for i := 1; i <= 2; i++ {
		if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i)}, EmployeeID: person.ID, SessionID: &session, StartedAt: day.Add(time.Duration(i) * time.Minute), Endpoint: "responses", ReqHashes: hash[:]}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	for {
		more, err := q.SnapshotNext(ctx, run)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	var blobs, segments int
	if err = st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wm_full_journal_blobs WHERE run_id=$1),(SELECT count(*) FROM wm_full_journal_segments s JOIN wm_full_journal_sources x ON x.id=s.source_id WHERE x.run_id=$1)`, run).Scan(&blobs, &segments); err != nil || blobs != 1 || segments != 2 {
		t.Fatalf("blobs=%d segments=%d %v", blobs, segments, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET plan_format='legacy' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if err = q.PlanRun(ctx, run, 4096); err != nil {
		t.Fatal(err)
	}
	var batchCount int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1`, run).Scan(&batchCount); err != nil || batchCount != 1 {
		t.Fatalf("small sources were not packed: %d %v", batchCount, err)
	}
}

func TestSnapshotRejectsCrossEmployeePrefix(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	public, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_prefix_public", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_prefix_private", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"content":"私密"}`)
	h := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: h, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	parent := [16]byte{1}
	session := int64(1)
	if err = st.InsertRequest(ctx, store.RequestRow{ID: parent, EmployeeID: private.ID, SessionID: &session, StartedAt: day.Add(time.Minute), Endpoint: "responses", ReqHashes: h[:]}); err != nil {
		t.Fatal(err)
	}
	if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{2}, EmployeeID: public.ID, SessionID: &session, StartedAt: day.Add(2 * time.Minute), Endpoint: "responses", ReqPrefixOf: &parent, ReqPrefixLen: 1}); err != nil {
		t.Fatal(err)
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.SnapshotNext(ctx, run); err != nil {
		t.Fatal(err)
	}
	var gaps, blobs int
	if err = st.Pool.QueryRow(ctx, `SELECT missing_count,(SELECT count(*) FROM wm_full_journal_blobs WHERE run_id=$1) FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&gaps, &blobs); err != nil || gaps != 1 || blobs != 0 {
		t.Fatalf("privacy gap=%d blobs=%d err=%v", gaps, blobs, err)
	}
}
