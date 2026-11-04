package wmjournal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workrecords"
)

type fakeModel struct {
	calls    int
	material string
	onCall   func()
}

type pairModel struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (m *pairModel) Extract(ctx context.Context, _ string) (string, string, Usage, error) {
	m.calls.Add(1)
	m.entered <- struct{}{}
	select {
	case <-m.release:
		return "source_id=1 事实", "mock", Usage{}, nil
	case <-ctx.Done():
		return "", "", Usage{}, ctx.Err()
	}
}

func TestPairClaimsDifferentBatchesAndProtectsUnknownCalls(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	employee, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "pair-member", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	var run, source int64
	var at time.Time
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,plan_format) VALUES('2026-10-29','running','session-evidence-v1') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'员工',1,now(),'responses','ready') RETURNING id,started_at`, run, employee.ID).Scan(&source, &at); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"role":"user","content":"处理项目问题"}`)
	hash := sha256.Sum256(data)
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',0,$2,'input')`, source, hash[:]); err != nil {
		t.Fatal(err)
	}
	sid := int64(1)
	var batches []workrecords.EvidenceBatch
	p, _ := workrecords.NewEvidencePlanner(100000, func(b workrecords.EvidenceBatch) error { batches = append(batches, b); return nil })
	if err = p.Add(workrecords.EvidenceGroup{EmployeeID: employee.ID, EmployeeName: "员工", SessionID: &sid, Direction: "request", Segment: workrecords.Segment{Hash: hash, Kind: "input", Data: data}, Occurrences: []workrecords.EvidenceOccurrence{{SourceID: source, Time: at}}}); err != nil {
		t.Fatal(err)
	}
	if err = p.Finish(); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(batches[0])
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,$2,'queued'),($1,'extract',1,$2,'queued')`, run, manifest); err != nil {
		t.Fatal(err)
	}
	m := &pairModel{entered: make(chan struct{}, 2), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, e := ProcessPair(ctx, st.Pool, run, m); done <- e }()
	for i := 0; i < 2; i++ {
		select {
		case <-m.entered:
		case <-time.After(5 * time.Second):
			close(m.release)
			t.Fatal("calls did not run concurrently")
		}
	}
	close(m.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	var completed, attempts int
	if err = st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1 AND state='done'),(SELECT count(*) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=$1)`, run).Scan(&completed, &attempts); err != nil || completed != 2 || attempts != 2 || m.calls.Load() != 2 {
		t.Fatalf("done=%d attempts=%d calls=%d err=%v", completed, attempts, m.calls.Load(), err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_full_journal_batches SET state='running' WHERE run_id=$1 AND ordinal=0`, run); err != nil {
		t.Fatal(err)
	}
	if _, err = ProcessPair(ctx, st.Pool, run, m); err == nil || m.calls.Load() != 2 {
		t.Fatal("unknown prior call was retried")
	}
}

func (m *fakeModel) Extract(_ context.Context, input string) (string, string, Usage, error) {
	m.calls++
	m.material = input
	if m.onCall != nil {
		m.onCall()
	}
	x, y := int64(12), int64(3)
	return "完成了工作 source_id 1", "mock", Usage{&x, &y}, nil
}

func TestWorkerPreservesAttemptsAndStopsForPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	employee, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "test-wmjournal", Name: "测试员工"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "test-wmjournal-other", Name: "其他员工"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET is_super_admin=true WHERE id=$1`, employee.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	cookie, csrf, err := st.CreateWebSession(ctx, employee.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer server.Close()
	var run, source int64
	var at time.Time
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,plan_format) VALUES('2026-10-29','running','session-evidence-v1') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'测试员工',1,now(),'responses','ready') RETURNING id,started_at`, run, employee.ID).Scan(&source, &at); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000002',$2,'其他员工',now(),'responses','missing')`, run, other.ID); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"type":"reasoning","summary":"完成测试","encrypted_content":"SECRET"}`)
	hash := sha256.Sum256(data)
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'output',$3)`, run, hash[:], data); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'response',0,$2,'output')`, source, hash[:]); err != nil {
		t.Fatal(err)
	}
	sid := int64(1)
	var batches []workrecords.EvidenceBatch
	planner, err := workrecords.NewEvidencePlanner(100000, func(b workrecords.EvidenceBatch) error { batches = append(batches, b); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = planner.Add(workrecords.EvidenceGroup{EmployeeID: employee.ID, EmployeeName: "测试员工", SessionID: &sid, Direction: "response", Segment: workrecords.Segment{Hash: hash, Kind: "output", Data: data}, Occurrences: []workrecords.EvidenceOccurrence{{SourceID: source, Ordinal: 0, Time: at}}}); err != nil {
		t.Fatal(err)
	}
	if err = planner.Finish(); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(batches[0])
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,$2,'queued'),($1,'extract',1,$2,'queued')`, run, manifest); err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{onCall: func() {
		request, _ := http.NewRequest(http.MethodPut, server.URL+"/api/log-worker", bytes.NewBufferString(`{"enabled":false}`))
		request.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		request.Header.Set("X-CSRF-Token", csrf)
		client := http.Client{Timeout: time.Second}
		response, e := client.Do(request)
		if e != nil {
			t.Errorf("pause was blocked: %v", e)
		} else {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Errorf("pause status %d", response.StatusCode)
			}
		}
		c, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, e := st.Pool.Exec(c, `UPDATE employees SET email='test@example.invalid' WHERE id=$1`, other.ID); e != nil {
			t.Errorf("unrelated employee was blocked: %v", e)
		}
		c2, cancel2 := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel2()
		if _, e := st.Pool.Exec(c2, `UPDATE employees SET private_work_since=now() WHERE id=$1`, employee.ID); e == nil {
			t.Error("batch owner's privacy changed during transmission")
		}
	}}
	ok, err := ProcessOne(ctx, st.Pool, run, model)
	if err != nil || !ok || model.calls != 1 || strings.Contains(model.material, "SECRET") || !strings.Contains(model.material, "完成测试") {
		t.Fatalf("processed %v calls %d error %v", ok, model.calls, err)
	}
	var done, attempts int
	if err = st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1 AND state='done'),(SELECT count(*) FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=$1)`, run).Scan(&done, &attempts); err != nil || done != 1 || attempts != 1 {
		t.Fatalf("done %d attempts %d error %v", done, attempts, err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, employee.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = ProcessOne(ctx, st.Pool, run, model); err == nil || model.calls != 1 {
		t.Fatalf("privacy did not stop worker: %v", err)
	}
}
