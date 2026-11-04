package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,plan_format) VALUES('2026-10-29','queued','session-evidence-v1') RETURNING id`).Scan(&run); e != nil {
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
	verified, e := q.VerifyEvidenceBatch(ctx, run, 0)
	if e != nil || verified.Parts != 1 || verified.Occurrences != 2 || verified.PayloadBytes == 0 {
		t.Fatalf("verify %+v %v", verified, e)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, emp.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = q.VerifyEvidenceBatch(ctx, run, 0); e == nil {
		t.Fatal("verified private evidence")
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=NULL WHERE id=$1`, emp.ID); e != nil {
		t.Fatal(e)
	}
	model := &evidenceCapture{}
	if ok, e := q.ProcessOnePaused(ctx, run, model); e != nil || !ok {
		t.Fatalf("process %v %v", ok, e)
	}
	if model.calls != 1 || !strings.Contains(model.input, string(data)) || !strings.Contains(model.input, `"ordinal":1`) {
		t.Fatalf("model did not receive full evidence: calls=%d", model.calls)
	}
	if _, e = q.ProcessOnePaused(ctx, run, model); e == nil || model.calls != 1 {
		t.Fatal("sample could run twice")
	}
	media := []byte(`{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}`)
	mediaHash := sha256.Sum256(media)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, mediaHash[:], media); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',2,$2,'input')`, source, mediaHash[:]); e != nil {
		t.Fatal(e)
	}
	var mediaBatch []EvidenceBatch
	mp, _ := NewEvidencePlanner(100000, func(b EvidenceBatch) error { mediaBatch = append(mediaBatch, b); return nil })
	if e = mp.Add(EvidenceGroup{EmployeeID: emp.ID, EmployeeName: "成员", SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: mediaHash, Data: media}, Occurrences: []EvidenceOccurrence{{SourceID: source, Ordinal: 2, Time: at}}}); e != nil {
		t.Fatal(e)
	}
	if e = mp.Finish(); e != nil {
		t.Fatal(e)
	}
	mediaManifest, _ := json.Marshal(mediaBatch[0])
	mediaTx, e := st.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = materializeEvidence(ctx, mediaTx, run, mediaManifest); !errors.Is(e, ErrEmbeddedMedia) {
		t.Fatalf("media did not stop before model: %v", e)
	}
	_ = mediaTx.Rollback(ctx)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',1,$2,'queued')`, run, mediaManifest); e != nil {
		t.Fatal(e)
	}
	if _, e = q.ProcessNext(ctx, run, model); !errors.Is(e, ErrEmbeddedMedia) || model.calls != 1 {
		t.Fatalf("media reached model: %v calls=%d", e, model.calls)
	}
	reasoning := []byte(`{"type":"reasoning","summary":[],"encrypted_content":"opaque-ciphertext"}`)
	reasoningHash := sha256.Sum256(reasoning)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, reasoningHash[:], reasoning); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',3,$2,'input')`, source, reasoningHash[:]); e != nil {
		t.Fatal(e)
	}
	var reasoningBatches []EvidenceBatch
	rp, _ := NewEvidencePlanner(100000, func(b EvidenceBatch) error { reasoningBatches = append(reasoningBatches, b); return nil })
	if e = rp.Add(EvidenceGroup{EmployeeID: emp.ID, EmployeeName: "成员", SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: reasoningHash, Data: reasoning}, Occurrences: []EvidenceOccurrence{{SourceID: source, Ordinal: 3, Time: at}}}); e != nil {
		t.Fatal(e)
	}
	if e = rp.Finish(); e != nil {
		t.Fatal(e)
	}
	reasoningManifest, _ := json.Marshal(reasoningBatches[0])
	reasoningTx, e := st.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = materializeEvidence(ctx, reasoningTx, run, reasoningManifest); !errors.Is(e, ErrOpaqueReasoning) {
		t.Fatalf("ciphertext did not stop before model: %v", e)
	}
	_ = reasoningTx.Rollback(ctx)
	// A mismatching snapshot must fail before claiming the batch or invoking a model.
	if _, e = st.Pool.Exec(ctx, `UPDATE wm_full_journal_blobs SET data='changed' WHERE run_id=$1`, run); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE wm_full_journal_batches SET input_manifest=$2 WHERE run_id=$1 AND phase='extract' AND ordinal=1`, run, manifest); e != nil {
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
