package wemanage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestReferenceAndManualPricing(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureSuperAdmin(ctx, "price-admin", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := st.GetEmployeeByOpenID(ctx, "price-admin")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, "测试应用")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i, model := range []string{"gpt-6.1-sol", "gpt-6.1-sol", "gpt-6.1-sol", "gpt-image-2"} {
		if err := st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i + 1)}, EmployeeID: svc.ID, StartedAt: start.Add(time.Duration(i) * time.Hour), ClientKind: "codex", Endpoint: "chat", ModelRequested: model, TokensInput: 1000000, TokensCached: 200000, TokensOutput: 500000, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	cookie, csrf, err := st.CreateWebSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&wemanage.Service{Store: st}).Handler())
	defer server.Close()
	post := func(body string, token string) int {
		req, _ := http.NewRequest("POST", server.URL+"/api/model-prices", bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
		req.Header.Set("X-CSRF-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	manual := fmt.Sprintf(`{"model":"gpt-6.1-sol","kind":"manual","currency":"CNY","input":10,"cached":2,"output":20,"effective_at":%q}`, start.Add(time.Hour).Format(time.RFC3339))
	if code := post(manual, ""); code != 403 {
		t.Fatalf("missing csrf: %d", code)
	}
	if code := post(manual, csrf); code != 200 {
		t.Fatalf("manual: %d", code)
	}
	reset := fmt.Sprintf(`{"model":"gpt-6.1-sol","kind":"standard","effective_at":%q}`, start.Add(2*time.Hour).Format(time.RFC3339))
	if code := post(reset, csrf); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if code := post(`{"model":"x","kind":"manual","currency":"USD","input":-1,"cached":0,"output":0,"effective_at":"2026-10-01T00:00:00Z"}`, csrf); code != 400 {
		t.Fatalf("negative: %d", code)
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/reporting/usage?month=2026-10", nil)
	req.AddCookie(&http.Cookie{Name: "aiseat_sess", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Rows []struct {
			Model    string   `json:"model"`
			Currency string   `json:"currency"`
			Cost     *float64 `json:"estimated_cost"`
			Priced   int      `json:"priced_requests"`
		} `json:"rows"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 200 || len(body.Rows) != 4 {
		t.Fatalf("response %d %+v %v", resp.StatusCode, body, err)
	}
	var usd, cny, unknown int
	for _, r := range body.Rows {
		switch r.Currency {
		case "USD":
			if r.Cost == nil || math.Abs(*r.Cost-6.62) > 0.00001 {
				t.Fatalf("reference %+v", r)
			}
			usd++
		case "CNY":
			if r.Cost == nil || math.Abs(*r.Cost-18.4) > 0.00001 {
				t.Fatalf("manual %+v", r)
			}
			cny++
		default:
			if r.Model != "gpt-image-2" || r.Cost != nil || r.Priced != 0 {
				t.Fatalf("unknown %+v", r)
			}
			unknown++
		}
	}
	if usd != 2 || cny != 1 || unknown != 1 {
		t.Fatalf("coverage: %+v", body.Rows)
	}
}
