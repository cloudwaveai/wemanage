package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestUnsupportedMediaStopsEntireScheduleBeforeModel(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	emp, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "media-stop", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	var run, source int64
	var at time.Time
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,plan_format) VALUES('2026-10-29','queued',$1) RETURNING id`, evidenceFormat).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'成员',1,now(),'responses','ready') RETURNING id,started_at`, run, emp.ID).Scan(&source, &at); e != nil {
		t.Fatal(e)
	}
	data := []byte(`{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}`)
	hash := sha256.Sum256(data)
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, hash[:], data); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) VALUES($1,'request',0,$2,'input')`, source, hash[:]); e != nil {
		t.Fatal(e)
	}
	sid := int64(1)
	var batches []EvidenceBatch
	p, _ := NewEvidencePlanner(100000, func(b EvidenceBatch) error { batches = append(batches, b); return nil })
	if e = p.Add(EvidenceGroup{EmployeeID: emp.ID, EmployeeName: "成员", SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: source, Ordinal: 0, Time: at}}}); e != nil {
		t.Fatal(e)
	}
	if e = p.Finish(); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(batches[0])
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',0,$2,'queued')`, run, raw); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE wm_full_journal_settings SET enabled=true,activated_at='2026-10-30 00:00:00+08' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		t.Fatal(e)
	}
	model := &fakeExtraction{}
	e = q.Tick(ctx, model, time.Date(2026, 10, 30, 4, 0, 0, 0, loc))
	if !errors.Is(e, ErrEmbeddedMedia) || model.calls != 0 {
		t.Fatalf("unexpected model or error: %v calls=%d", e, model.calls)
	}
	var enabled bool
	var state, message string
	var attempts int
	if e = st.Pool.QueryRow(ctx, `SELECT s.enabled,r.state,r.error,(SELECT count(*) FROM wm_full_journal_attempts) FROM wm_full_journal_settings s JOIN wm_full_journal_runs r ON r.id=$1 WHERE s.id=1`, run).Scan(&enabled, &state, &message, &attempts); e != nil || enabled || state != "failed" || !strings.Contains(message, "图片") || attempts != 0 {
		t.Fatalf("guard %v %s %q %d: %v", enabled, state, message, attempts, e)
	}
}
