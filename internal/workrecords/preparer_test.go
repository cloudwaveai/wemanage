package workrecords

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestLocalPreparerRespectsPauseAndHandsOffWithoutModelCalls(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := &Queue{Store: st}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_local_prep", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	data := []byte(`{"content":"完整内容"}`)
	h := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: h, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{21}, EmployeeID: person.ID, StartedAt: day.Add(time.Hour), Endpoint: "responses", ReqHashes: h[:]}); err != nil {
		t.Fatal(err)
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, steps, err := q.PrepareAvailable(ctx); err != nil || steps != 0 {
		t.Fatalf("disabled worker: %d %v", steps, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	guard, err := st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Exec(ctx, `SELECT pg_advisory_lock(7419752105)`); err != nil {
		t.Fatal(err)
	}
	if _, steps, err := q.PrepareAvailable(ctx); err != nil || steps != 0 {
		t.Fatalf("scheduler lock: %d %v", steps, err)
	}
	if _, err = guard.Exec(ctx, `SELECT pg_advisory_unlock(7419752105)`); err != nil {
		t.Fatal(err)
	}
	guard.Release()
	got, steps, err := q.PrepareAvailable(ctx)
	if err != nil || got != run || steps < 2 {
		t.Fatalf("prepared run=%d steps=%d err=%v", got, steps, err)
	}
	var state string
	var sources, attempts int
	if err = st.Pool.QueryRow(ctx, `SELECT r.state,(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=r.id AND status='ready'),(SELECT count(*) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=r.id) FROM wm_full_journal_runs r WHERE id=$1`, run).Scan(&state, &sources, &attempts); err != nil || state != "queued" || sources != 1 || attempts != 0 {
		t.Fatalf("handoff state=%s sources=%d attempts=%d err=%v", state, sources, attempts, err)
	}
	model := &fakeExtraction{}
	if _, err = q.Step(ctx, run, model, "gpt-6.1-sol"); err != nil || model.calls != 1 {
		t.Fatalf("gateway model handoff calls=%d err=%v", model.calls, err)
	}
}

func TestLocalPreparerStopsOnPauseAndResumes(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := &Queue{Store: st}
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_local_pause", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	data := []byte(`{"content":"内容"}`)
	h := sha256.Sum256(data)
	if err = st.PutBlobs(ctx, []store.Blob{{Hash: h, Kind: "input", Data: data}}, day); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i)}, EmployeeID: person.ID, StartedAt: day.Add(time.Duration(i) * time.Minute), Endpoint: "responses", ReqHashes: h[:]}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := q.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, _, e := q.PrepareAvailable(ctx); result <- e }()
	deadline := time.Now().Add(5 * time.Second)
	ready := 0
	for time.Now().Before(deadline) {
		if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND status='ready'`, run).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready == 0 {
		t.Fatal("worker did not start")
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=false WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	var pausedCount int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND status='ready'`, run).Scan(&pausedCount); err != nil || pausedCount >= 20 {
		t.Fatalf("pause count=%d err=%v", pausedCount, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = q.PrepareAvailable(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	var total int
	if err = st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND status='ready') FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state, &total); err != nil || state != "queued" || total != 20 {
		t.Fatalf("resumed state=%s count=%d err=%v", state, total, err)
	}
}
