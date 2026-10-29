package store_test

import (
	"context"
	"math"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestProjectUsagePricesByTimeAndLeavesUnknownUnpriced(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	svc, err := st.CreateServiceAccount(ctx, "service")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(ctx, "客户 A", "client")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateAPIChannel(ctx, store.APIChannel{Name: "upstream", Protocol: "openai", BaseURL: "https://example.com/v1", CredentialCipher: []byte("opaque")})
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := st.IssueServiceKey(ctx, svc.ID, "app", &ch.ID, &proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.AddChannelRate(ctx, store.ChannelRate{ChannelID: ch.ID, Model: "model-a", Currency: "CNY", Input: 10, Cached: 2, Output: 20, EffectiveAt: start}); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{start.Add(-time.Hour), start.Add(time.Hour)} {
		if err := st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i + 1)}, EmployeeID: svc.ID, APIKeyID: &key.ID, StartedAt: at, ClientKind: "other", Endpoint: "chat", ModelRequested: "model-a", TokensInput: 1000000, TokensCached: 200000, TokensOutput: 500000, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ProjectUsage(ctx, start.Add(-24*time.Hour), start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	var priced, unknown bool
	for _, r := range rows {
		if r.ProjectID == nil || *r.ProjectID != proj.ID || r.EmployeeID != svc.ID {
			t.Fatalf("attribution=%+v", r)
		}
		if r.Currency == "CNY" {
			priced = true
			if r.PricedRequests != 1 || math.Abs(r.EstimatedCost-18.4) > 0.00001 {
				t.Fatalf("cost=%+v", r)
			}
		} else {
			unknown = true
			if r.PricedRequests != 0 {
				t.Fatalf("unpriced=%+v", r)
			}
		}
	}
	if !priced || !unknown {
		t.Fatalf("rows=%+v", rows)
	}
}
