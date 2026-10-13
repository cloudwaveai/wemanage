package web_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"aiseat/internal/store"
)

func TestWorkboard(t *testing.T) {
	ts, st, ctx := newServer(t)
	_ = st.EnsureSuperAdmin(ctx, "ou_boss", "boss")
	boss := login(t, ts, "ou_boss")
	alice := login(t, ts, "ou_alice")
	carol, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_carol", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	_ = carol

	ids := map[string]int64{}
	for _, oid := range []string{"ou_boss", "ou_alice"} {
		e, err := st.GetEmployeeByOpenID(ctx, oid)
		if err != nil {
			t.Fatal(err)
		}
		ids[oid] = e.ID
	}
	now := time.Now()
	add := func(emp int64, in, out int64, model string) {
		t.Helper()
		sid := int64(1)
		if err := st.InsertRequest(ctx, store.RequestRow{ID: uuid.New(), StartedAt: now.Add(-time.Hour), EmployeeID: emp,
			SessionID: &sid, ClientKind: "codex", Endpoint: "responses", ModelRequested: model, StatusCode: 200,
			TokensInput: in, TokensOutput: out}); err != nil {
			t.Fatal(err)
		}
	}
	add(ids["ou_alice"], 1000, 200, "gpt-6-luna")
	add(ids["ou_alice"], 500, 100, "gpt-6-sol")
	add(ids["ou_boss"], 100, 10, "gpt-6-sol")

	// Employees cannot see it.
	if resp, _ := alice.do(http.MethodGet, "/api/admin/workboard", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("employee got %d, want 403", resp.StatusCode)
	}

	resp, body := boss.do(http.MethodGet, "/api/admin/workboard?range=7d", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Totals struct {
			TokensTotal     int64 `json:"tokens_total"`
			ActiveEmployees int   `json:"active_employees"`
		}
		Employees []struct {
			Name        string
			TokensTotal int64  `json:"tokens_total"`
			Requests    int64  `json:"requests"`
			TopModel    string `json:"top_model"`
		}
		Idle  []struct{ Name string }
		Daily []struct{ Tokens int64 }
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.TokensTotal != 1910 || got.Totals.ActiveEmployees != 2 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if len(got.Employees) != 2 || got.Employees[0].Name != "ou_alice" || got.Employees[0].TokensTotal != 1800 ||
		got.Employees[0].Requests != 2 || got.Employees[0].TopModel != "gpt-6-luna" {
		t.Fatalf("employees = %+v", got.Employees)
	}
	if len(got.Idle) != 1 || got.Idle[0].Name != "carol" {
		t.Fatalf("idle = %+v", got.Idle)
	}
	if len(got.Daily) != 7 {
		t.Fatalf("daily has %d points, want 7", len(got.Daily))
	}

	// The day's summaries, as the board reads them.
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)
	if err := st.StartWorkDigestDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: ids["ou_alice"], SummaryMD: "改登录页", Sessions: 2, Messages: 5}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkDigestDay(ctx, day, "团队在做登录页", "gpt-6-luna", "done", ""); err != nil {
		t.Fatal(err)
	}
	resp, body = boss.do(http.MethodGet, "/api/admin/workboard/digests", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var dg struct {
		Day        string
		Status     string
		OverviewMD string `json:"overview_md"`
		Configured bool
		Days       []string
		Employees  []struct {
			Name      string
			SummaryMD string `json:"summary_md"`
			Messages  int
		}
	}
	if err := json.Unmarshal(body, &dg); err != nil {
		t.Fatal(err)
	}
	if dg.Day != "2026-10-13" || dg.Status != "done" || dg.OverviewMD != "团队在做登录页" || dg.Configured ||
		len(dg.Days) != 1 || len(dg.Employees) != 1 || dg.Employees[0].Name != "ou_alice" || dg.Employees[0].Messages != 5 {
		t.Fatalf("digests = %s", body)
	}
	if resp, _ := boss.do(http.MethodPost, "/api/admin/workboard/digests/run", `{"day":"2026-10-13"}`); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("run without a runner: %d", resp.StatusCode)
	}
}
