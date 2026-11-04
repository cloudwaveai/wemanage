package wemanage_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestUsageReportSeparatesUnknownPrice(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "report-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := st.GetEmployeeByOpenID(ctx, "report-admin")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, "测试应用")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(ctx, "客户 A", "client")
	if err != nil {
		t.Fatal(err)
	}
	channel, err := st.CreateAPIChannel(ctx, store.APIChannel{Name: "测试", Protocol: "openai", BaseURL: "https://example.invalid/v1", CredentialCipher: []byte("opaque")})
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := st.IssueServiceKey(ctx, svc.ID, "test", &channel.ID, &project.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if err = st.AddChannelRate(ctx, store.ChannelRate{ChannelID: channel.ID, Model: "model-a", Currency: "CNY", Input: 10, Cached: 2, Output: 20, EffectiveAt: start}); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{start.Add(-time.Hour), start.Add(time.Hour)} {
		if err = st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i + 1)}, EmployeeID: svc.ID, APIKeyID: &key.ID, StartedAt: at, ClientKind: "other", Endpoint: "chat", ModelRequested: "model-a", TokensInput: 1000000, TokensCached: 200000, TokensOutput: 500000, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	cookie, _, err := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/reporting/usage?month=2026-10", nil)
	req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Rows []struct {
			Currency string   `json:"currency"`
			Priced   int      `json:"priced_requests"`
			Cost     *float64 `json:"estimated_cost"`
		} `json:"rows"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 200 || len(body.Rows) != 2 {
		t.Fatalf("status %d rows %+v err %v", resp.StatusCode, body.Rows, err)
	}
	var priced, unknown bool
	for _, r := range body.Rows {
		if r.Currency == "CNY" {
			priced = r.Priced == 1 && r.Cost != nil && math.Abs(*r.Cost-18.4) < 0.0001
		} else {
			unknown = r.Priced == 0 && r.Cost == nil
		}
	}
	if !priced || !unknown {
		t.Fatalf("incorrect pricing %+v", body.Rows)
	}
}
