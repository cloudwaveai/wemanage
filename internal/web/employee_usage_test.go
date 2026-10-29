package web_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"aiseat/internal/store"
	"github.com/google/uuid"
)

func TestEmployeeWorkUsageHourlyAndPrivacy(t *testing.T) {
	ts, st, ctx := newServer(t)
	if err := st.EnsureSuperAdmin(ctx, "ou_mgr_detail", "manager"); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "ou_mgr_detail")
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_detail", Name: "member"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_other_detail", Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_owner_detail", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)
	for i, item := range []struct {
		emp           int64
		hour          int
		input, output int64
		endpoint      string
	}{{person.ID, 9, 100, 20, "responses"}, {person.ID, 9, 25, 5, "/local/codex-import"}, {person.ID, 15, 50, 10, "responses"}, {other.ID, 9, 999, 999, "responses"}} {
		if err := st.InsertRequest(ctx, store.RequestRow{ID: uuid.New(), EmployeeID: item.emp, StartedAt: day.Add(time.Duration(item.hour) * time.Hour), ClientKind: "codex", Endpoint: item.endpoint, StatusCode: 200, ModelRequested: fmt.Sprintf("model-%d", i%2), TokensInput: item.input, TokensOutput: item.output}); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/admin/workboard/employees/%d?day=2026-10-10", person.ID)
	resp, body := admin.do(http.MethodGet, path, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d %s", resp.StatusCode, body)
	}
	var got struct {
		Requests int64 `json:"requests"`
		Imported int64 `json:"imported_records"`
		Tokens   int64 `json:"tokens_total"`
		Hourly   []struct {
			T        time.Time `json:"t"`
			Requests int64     `json:"requests"`
			Tokens   int64     `json:"tokens"`
		} `json:"hourly"`
		Models []struct {
			Key string `json:"key"`
		} `json:"by_model"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Requests != 3 || got.Imported != 1 || got.Tokens != 210 || len(got.Hourly) != 24 || len(got.Models) != 2 {
		t.Fatalf("summary=%+v", got)
	}
	if got.Hourly[9].Tokens != 150 || got.Hourly[9].Requests != 2 || got.Hourly[15].Tokens != 60 || got.Hourly[10].Tokens != 0 {
		t.Fatalf("hourly=%+v", got.Hourly)
	}
	resp, _ = login(t, ts, "ou_regular_detail").do(http.MethodGet, path, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: %d", resp.StatusCode)
	}
	resp, _ = admin.do(http.MethodGet, fmt.Sprintf("/api/admin/workboard/employees/%d?day=2026-10-10", owner.ID), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("private employee: %d", resp.StatusCode)
	}
	resp, _ = admin.do(http.MethodGet, fmt.Sprintf("/api/admin/workboard/employees/%d?day=bad", person.ID), "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad date: %d", resp.StatusCode)
	}
}
