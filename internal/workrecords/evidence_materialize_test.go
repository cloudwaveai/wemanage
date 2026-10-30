package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type evidenceCapture struct {
	calls int
	input string
}

func (f *evidenceCapture) Extract(_ context.Context, _, input string) (string, string, workdigest.ModelUsage, error) {
	f.calls++
	f.input = input
	return "工作摘要", "fake", workdigest.ModelUsage{}, nil
}
func TestEvidenceModelReceivesVerifiedSnapshotText(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	emp, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "evidence-owner", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	var run, source int64
	var at time.Time
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','queued') RETURNING id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'成员',7,now(),'responses','ready') RETURNING id,started_at`, run, emp.ID).Scan(&source, &at); e != nil {
		t.Fatal(e)
	}
	data := []byte("这是完整上下文")
	hash := sha256.Sum256(data)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',0,$2,'input'),($1,'request',1,$2,'input')`, source, hash[:]); e != nil {
		t.Fatal(e)
	}
	sid := int64(7)
	var batches []EvidenceBatch
	p, _ := NewEvidencePlanner(100000, func(b EvidenceBatch) error { batches = append(batches, b); return nil })
	if e = p.Add(EvidenceGroup{EmployeeID: emp.ID, EmployeeName: "成员", SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: source, Ordinal: 0, Time: at}, {SourceID: source, Ordinal: 1, Time: at}}}); e != nil {
		t.Fatal(e)
	}
	if e = p.Finish(); e != nil {
		t.Fatal(e)
	}
	manifest, _ := json.Marshal(batches[0])
	if strings.Contains(string(manifest), string(data)) {
		t.Fatal("manifest duplicates text")
	}
	bad := batches[0]
	bad.Parts = append([]EvidencePart(nil), bad.Parts...)
	bad.Parts[0].Occurrences = append([]EvidenceOccurrence(nil), bad.Parts[0].Occurrences...)
	bad.Parts[0].Occurrences[1].Ordinal = 99
	corrupted, _ := json.Marshal(bad)
	tx, e := st.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = materializeEvidence(ctx, tx, run, corrupted); e == nil {
		t.Fatal("accepted mismatched occurrence")
	}
	_ = tx.Rollback(ctx)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,$2,'queued')`, run, manifest); e != nil {
		t.Fatal(e)
	}
	model := &evidenceCapture{}
	if ok, e := q.ProcessNext(ctx, run, model); e != nil || !ok {
		t.Fatalf("process %v %v", ok, e)
	}
	if model.calls != 1 || !strings.Contains(model.input, string(data)) || !strings.Contains(model.input, `"ordinal":1`) {
		t.Fatalf("model did not receive full evidence: calls=%d", model.calls)
	}
	// A mismatching snapshot must fail before claiming the batch or invoking a model.
	if _, e = st.Pool.Exec(ctx, `UPDATE wm_full_journal_blobs SET data='changed' WHERE run_id=$1`, run); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',1,$2,'queued')`, run, manifest); e != nil {
		t.Fatal(e)
	}
	if _, e = q.ProcessNext(ctx, run, model); e == nil || model.calls != 1 {
		t.Fatalf("mismatch reached model: %v calls=%d", e, model.calls)
	}
	var state string
	if e = st.Pool.QueryRow(ctx, `SELECT state FROM wm_full_journal_batches WHERE run_id=$1 AND ordinal=1`, run).Scan(&state); e != nil || state != "queued" {
		t.Fatalf("bad claim %s %v", state, e)
	}
}
