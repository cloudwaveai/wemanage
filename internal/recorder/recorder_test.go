package recorder_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/recorder"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"

	"github.com/google/uuid"
)

func setup(t *testing.T) (*store.Store, *recorder.Recorder, context.Context, string) {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	spool := t.TempDir()
	rec := recorder.New(st, set, spool, nil, 128)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return st, rec, ctx, spool
}

func mkEmp(t *testing.T, st *store.Store, ctx context.Context) int64 {
	t.Helper()
	e, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_r", Name: "r"})
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func respBody(endpoint string, inputs []string) []byte {
	items := make([]any, len(inputs))
	for i, s := range inputs {
		items[i] = map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": s}},
		}
	}
	body := map[string]any{"model": "gpt-5.1-codex", "instructions": "sys", "input": items}
	b, _ := json.Marshal(body)
	return b
}

func outcome(text string) capture.Outcome {
	item, _ := json.Marshal(map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	})
	return capture.Outcome{
		Model:       "gpt-5.1-codex",
		Usage:       capture.Usage{Input: 10, Output: 5},
		OutputItems: []json.RawMessage{item},
		Completed:   true,
	}
}

func newID() [16]byte { return uuid.New() }

func TestRecorderDedupAndPrefixChain(t *testing.T) {
	st, rec, ctx, _ := setup(t)
	emp := mkEmp(t, st, ctx)
	rec.Start(ctx)

	id1 := newID()
	rec.Submit(recorder.Record{
		RequestID: id1, StartedAt: time.Now(), EmployeeID: emp,
		SessionKey: "sess-1", ClientKind: "codex", Endpoint: "responses",
		ReqBody: respBody("responses", []string{"hello"}), Outcome: outcome("hi"), StatusCode: 200,
	})
	rec.Flush()

	// Second request in the same session appends one input element.
	id2 := newID()
	rec.Submit(recorder.Record{
		RequestID: id2, StartedAt: time.Now(), EmployeeID: emp,
		SessionKey: "sess-1", ClientKind: "codex", Endpoint: "responses",
		ReqBody: respBody("responses", []string{"hello", "world"}), Outcome: outcome("ok"), StatusCode: 200,
	})
	rec.Flush()

	// Second request must be prefix-compressed onto the first.
	enc2, err := st.GetRequestEnc(ctx, id2)
	if err != nil {
		t.Fatal(err)
	}
	if enc2.PrefixOf == nil || *enc2.PrefixOf != id1 {
		t.Fatalf("expected req2 to prefix req1, got %+v", enc2.PrefixOf)
	}

	// Reconstruct req2's full hash list via the chain and verify each blob exists.
	full, err := capture.Decode(toCapture(enc2), func(id capture.UUID) (capture.Encoded, error) {
		e, err := st.GetRequestEnc(ctx, [16]byte(id))
		if err != nil {
			return capture.Encoded{}, err
		}
		return toCapture(e), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range full {
		if _, _, err := st.GetBlob(ctx, h); err != nil {
			t.Fatalf("blob %x missing: %v", h, err)
		}
	}

	// request_texts for req2 should contain only the NEW user input ("world"), not "hello".
	var texts []string
	rows, _ := st.Pool.Query(ctx, `SELECT text FROM request_texts WHERE request_id=$1 AND role='user'`, id2[:])
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		texts = append(texts, s)
	}
	rows.Close()
	if len(texts) != 1 || texts[0] != "world" {
		t.Fatalf("expected only new input 'world', got %v", texts)
	}
}

func TestRecorderSpoolReplay(t *testing.T) {
	st, rec, ctx, spool := setup(t)
	emp := mkEmp(t, st, ctx)

	// Write a record directly to the spool file (simulating a prior DB outage).
	r := recorder.Record{
		RequestID: newID(), StartedAt: time.Now(), EmployeeID: emp,
		SessionKey: "sess-x", ClientKind: "codex", Endpoint: "responses",
		ReqBody: respBody("responses", []string{"spooled"}), Outcome: outcome("y"), StatusCode: 200,
	}
	b, _ := json.Marshal(r)
	if err := os.WriteFile(spool+"/pending.jsonl", append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	rec.Start(ctx) // replays spool at startup

	// Give the replay a moment (Start replays synchronously before the worker loop).
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests WHERE id=$1`, r.RequestID[:]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("spooled record not replayed, count=%d", n)
	}
	if _, err := os.Stat(spool + "/pending.jsonl"); !os.IsNotExist(err) {
		t.Fatal("spool file should be gone after successful replay")
	}
}

func TestPurgeKeepsReferenced(t *testing.T) {
	st, rec, ctx, _ := setup(t)
	emp := mkEmp(t, st, ctx)
	rec.Start(ctx)

	old := time.Now().Add(-200 * 24 * time.Hour)
	rec.Submit(recorder.Record{
		RequestID: newID(), StartedAt: old, EmployeeID: emp,
		SessionKey: "old", ClientKind: "codex", Endpoint: "responses",
		ReqBody: respBody("responses", []string{"ancient"}), Outcome: outcome("z"), StatusCode: 200,
	})
	rec.Flush()

	id2 := newID()
	rec.Submit(recorder.Record{
		RequestID: id2, StartedAt: time.Now(), EmployeeID: emp,
		SessionKey: "new", ClientKind: "codex", Endpoint: "responses",
		ReqBody: respBody("responses", []string{"fresh"}), Outcome: outcome("z"), StatusCode: 200,
	})
	rec.Flush()

	cutoff := time.Now().Add(-180 * 24 * time.Hour)
	if _, err := st.PurgeExpiredBlobs(ctx, cutoff); err != nil {
		t.Fatal(err)
	}

	// The fresh request's segments must still be reconstructable.
	enc, _ := st.GetRequestEnc(ctx, id2)
	full, err := capture.Decode(toCapture(enc), func(id capture.UUID) (capture.Encoded, error) {
		e, err := st.GetRequestEnc(ctx, [16]byte(id))
		return toCapture(e), err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range full {
		if _, _, err := st.GetBlob(ctx, h); err != nil {
			t.Fatalf("fresh blob purged: %v", err)
		}
	}
}

func toCapture(e store.RequestEnc) capture.Encoded {
	var out capture.Encoded
	out.PrefixLen = e.PrefixLen
	out.ChainDepth = e.ChainDepth
	out.Hashes = e.Hashes
	if e.PrefixOf != nil {
		u := capture.UUID(*e.PrefixOf)
		out.PrefixOf = &u
	}
	return out
}
