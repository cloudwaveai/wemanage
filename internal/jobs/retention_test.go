package jobs_test

import (
	"context"
	"testing"
	"time"

	"aiseat/internal/jobs"
	"aiseat/internal/recorder"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"

	"github.com/google/uuid"
)

func TestRetentionDeletesOldContentKeepsMetadata(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	ctx := context.Background()

	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_r", Name: "r"})
	rec := recorder.New(st, set, t.TempDir(), nil, 16)
	rec.Start(ctx)

	old := time.Now().Add(-200 * 24 * time.Hour)
	oldID := uuid.New()
	rec.Submit(recorder.Record{
		RequestID: oldID, StartedAt: old, EmployeeID: emp.ID, SessionKey: "old",
		ClientKind: "codex", Endpoint: "responses", StatusCode: 200,
		ReqBody: []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"ancient secret"}]}]}`),
	})
	rec.Flush()

	if err := jobs.Retention(ctx, st, 180, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetRequestFull(ctx, oldID)
	if err != nil || before.BodiesPurged {
		t.Fatalf("default retention must preserve content: %+v %v", before, err)
	}
	var retained int
	if err := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_texts`).Scan(&retained); err != nil || retained == 0 {
		t.Fatalf("expected retained texts: %d %v", retained, err)
	}
	if err := jobs.Retention(ctx, st, 180, true, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Metadata row remains, but flagged purged.
	row, err := st.GetRequestFull(ctx, oldID)
	if err != nil {
		t.Fatalf("request metadata should remain: %v", err)
	}
	if !row.BodiesPurged {
		t.Fatal("old request should be marked bodies_purged")
	}
	// Its blobs are gone.
	var blobCount int
	if err := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM blobs`).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 0 {
		t.Fatalf("expected old blobs purged, got %d", blobCount)
	}
	// Searchable text gone too.
	var textCount int
	_ = st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_texts`).Scan(&textCount)
	if textCount != 0 {
		t.Fatalf("expected old texts purged, got %d", textCount)
	}
}
