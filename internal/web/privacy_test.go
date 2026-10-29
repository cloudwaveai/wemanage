package web_test

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"github.com/google/uuid"
)

func TestPrivateWorkIsHiddenFromOtherAdminsAndSummaries(t *testing.T) {
	ts, st, ctx := newServer(t)
	if err := st.EnsureSuperAdmin(ctx, "ou_reader", "reader"); err != nil {
		t.Fatal(err)
	}
	reader := login(t, ts, "ou_reader")
	owner, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_owner", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id := uuid.New()
	if err := st.InsertRequest(ctx, store.RequestRow{ID: id, StartedAt: now, EmployeeID: owner.ID, ClientKind: "codex", Endpoint: "responses", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTexts(ctx, []store.TextRow{{RequestID: id, EmployeeID: owner.ID, StartedAt: now, Role: "user", Text: "private unique text"}}); err != nil {
		t.Fatal(err)
	}
	resp, body := reader.do(http.MethodGet, "/api/logs?q=private", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "林伊默") || strings.Contains(string(body), hex.EncodeToString(id[:])) {
		t.Fatalf("list leaked: %d %s", resp.StatusCode, body)
	}
	resp, _ = reader.do(http.MethodGet, "/api/logs/"+hex.EncodeToString(id[:]), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("detail status %d", resp.StatusCode)
	}
	ids, err := st.TextEmployees(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range ids {
		if v == owner.ID {
			t.Fatal("owner included in digest generation")
		}
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if err := st.StartWorkDigestDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: owner.ID, SummaryMD: "private summary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkDigestDay(ctx, day, "old private overview", "model", "done", ""); err != nil {
		t.Fatal(err)
	}
	// A newly enabled privacy policy makes previously generated overviews unsafe.
	if _, err := st.Pool.Exec(ctx, `UPDATE work_digest_days SET privacy_safe=false WHERE day=$1`, day); err != nil {
		t.Fatal(err)
	}
	resp, body = reader.do(http.MethodGet, "/api/admin/workboard/digests?day="+day.Format(time.DateOnly), "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "private summary") || strings.Contains(string(body), "old private overview") {
		t.Fatalf("digest leaked: %d %s", resp.StatusCode, body)
	}
	if err := st.PruneWorkDigests(ctx, day, nil); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM work_digests WHERE employee_id=$1`, owner.ID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("private summary not retained: %d %v", retained, err)
	}
}
