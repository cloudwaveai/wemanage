package web_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"github.com/google/uuid"
)

func TestProjectsPreserveHistoryAndPrivateEvents(t *testing.T) {
	ts, st, ctx := newServer(t)
	if err := st.EnsureSuperAdmin(ctx, "ou_mgr", "manager"); err != nil {
		t.Fatal(err)
	}
	mgr := login(t, ts, "ou_mgr")
	person, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_project", Name: "member"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_hidden", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, "客户 A", "client")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.CreateProject(ctx, "基础建设", "infrastructure")
	if err != nil {
		t.Fatal(err)
	}
	reqID := uuid.New()
	if err := st.InsertRequest(ctx, store.RequestRow{ID: reqID, EmployeeID: person.ID, StartedAt: time.Now(), Endpoint: "responses", ClientKind: "codex", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/requests/" + hex.EncodeToString(reqID[:]) + "/project"
	for _, id := range []int64{p.ID, p2.ID} {
		resp, body := mgr.do(http.MethodPost, path, fmt.Sprintf(`{"project_id":%d}`, id))
		if resp.StatusCode != 200 {
			t.Fatalf("assign %d %s", resp.StatusCode, body)
		}
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM request_project_history WHERE request_id=$1`, reqID[:]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history %d %v", count, err)
	}
	eventPath := fmt.Sprintf("/api/admin/projects/%d/events", p.ID)
	body := fmt.Sprintf(`{"employee_id":%d,"occurred_at":"2026-10-01T09:30:00+08:00","content":"过程与成果","evidence":"doc-ref"}`, person.ID)
	resp, out := mgr.do(http.MethodPost, eventPath, body)
	if resp.StatusCode != 200 {
		t.Fatalf("event %d %s", resp.StatusCode, out)
	}
	resp, out = mgr.do(http.MethodGet, eventPath, "")
	if resp.StatusCode != 200 || !strings.Contains(string(out), "过程与成果") {
		t.Fatalf("event list %d %s", resp.StatusCode, out)
	}
	body = fmt.Sprintf(`{"employee_id":%d,"occurred_at":"2026-10-01T09:30:00+08:00","content":"private"}`, owner.ID)
	resp, _ = mgr.do(http.MethodPost, eventPath, body)
	if resp.StatusCode != 403 {
		t.Fatalf("private event status %d", resp.StatusCode)
	}
	other := login(t, ts, "ou_regular")
	resp, _ = other.do(http.MethodGet, "/api/admin/projects", "")
	if resp.StatusCode != 403 {
		t.Fatalf("unprivileged %d", resp.StatusCode)
	}
}
func TestChannelAdminDoesNotExposeSecretsOrAcceptMissingPrices(t *testing.T) {
	ts, st, ctx := newServer(t)
	if err := st.EnsureSuperAdmin(ctx, "ou_ch_admin", "admin"); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "ou_ch_admin")
	secret := "synthetic-test-secret"
	resp, body := admin.do(http.MethodPost, "/api/admin/api-channels", `{"name":"test","protocol":"openai","base_url":"https://example.com/v1","credential":"`+secret+`"}`)
	if resp.StatusCode != 200 || strings.Contains(string(body), secret) {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var c struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do(http.MethodGet, "/api/admin/api-channels", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), secret) {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var cipher []byte
	if err := st.Pool.QueryRow(ctx, `SELECT credential_cipher FROM api_channels WHERE id=$1`, c.ID).Scan(&cipher); err != nil || strings.Contains(string(cipher), secret) {
		t.Fatalf("credential not sealed: %v", err)
	}
	path := fmt.Sprintf("/api/admin/api-channels/%d/rates", c.ID)
	resp, _ = admin.do(http.MethodPost, path, `{"model":"m","currency":"CNY","input_per_million":1,"output_per_million":2,"effective_at":"2026-01-01T00:00:00Z"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("missing cached price status %d", resp.StatusCode)
	}
}
