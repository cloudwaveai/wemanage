package workrecords

import (
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func TestImageAuditIsReadOnlyAndFailsOnPrivateSources(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	q := Queue{Store: st}
	emp, e := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "image-audit", Name: "成员"})
	if e != nil {
		t.Fatal(e)
	}
	var run int64
	if e = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','preparing') RETURNING id`).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,'00000000-0000-0000-0000-000000000001',$2,'成员',now(),'responses','ready')`, run, emp.ID); e != nil {
		t.Fatal(e)
	}
	var im bytes.Buffer
	if e = png.Encode(&im, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	good := []byte(`{"image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(im.Bytes()) + `"}`)
	bad := []byte(`{"image":"data:image/png;base64,YWJj"}`)
	for _, data := range [][]byte{good, bad} {
		h := sha256.Sum256(data)
		if _, e = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) VALUES($1,$2,'input',$3)`, run, h[:], data); e != nil {
			t.Fatal(e)
		}
	}
	result, e := q.AuditInlineImages(ctx, run)
	if e != nil || result.Fragments != 2 || result.ReadableImages != 1 || result.UnverifiedFragments != 1 || result.OriginalImageBytes != int64(im.Len()) {
		t.Fatalf("audit %+v %v", result, e)
	}
	if _, e = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, emp.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = q.AuditInlineImages(ctx, run); e == nil {
		t.Fatal("audited private sources")
	}
}
