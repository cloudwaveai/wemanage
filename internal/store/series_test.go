package store_test

import (
	"testing"
	"time"

	"aiseat/internal/store"
)

func TestUsageBreaksTokensDownByModel(t *testing.T) {
	st, ctx := newStore(t)
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_by_model", Name: "m"})
	now := time.Now()
	rows := []store.RequestRow{
		{ModelRequested: "gpt-6-luna", TokensInput: 100, TokensCached: 40, TokensOutput: 20, TokensReasoning: 10},
		{ModelRequested: "gpt-6-luna", TokensInput: 50, TokensCached: 10, TokensOutput: 5},
		{ModelRequested: "gpt-6-sol", TokensInput: 10, TokensOutput: 30, TokensReasoning: 20},
	}
	for i := range rows {
		rows[i].ID = [16]byte{byte(i + 1)}
		rows[i].StartedAt = now
		rows[i].EmployeeID = emp.ID
		rows[i].ClientKind = "codex"
		rows[i].Endpoint = "responses"
		rows[i].StatusCode = 200
		if err := st.InsertRequest(ctx, rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.Usage(ctx, now.Add(-time.Hour), now.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.ByModel) != 2 || u.ByModel[0].Key != "gpt-6-luna" {
		t.Fatalf("by model = %+v", u.ByModel)
	}
	luna := u.ByModel[0]
	if luna.Requests != 2 || luna.TokensTotal != 175 || luna.TokensInput != 150 ||
		luna.TokensCached != 50 || luna.TokensOutput != 25 || luna.TokensReasoning != 10 {
		t.Fatalf("luna = %+v", luna)
	}
	if sol := u.ByModel[1]; sol.TokensTotal != 40 || sol.TokensInput != 10 || sol.TokensOutput != 30 || sol.TokensReasoning != 20 {
		t.Fatalf("sol = %+v", sol)
	}
}

func TestUsageSeriesBucketsAndFillsGaps(t *testing.T) {
	st, ctx := newStore(t)
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_series", Name: "s"})
	loc := time.FixedZone("CST", 8*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 9, 29, h, m, 0, 0, loc) }
	for i, ts := range []time.Time{at(10, 2), at(10, 7), at(11, 31)} {
		if err := st.InsertRequest(ctx, store.RequestRow{ID: [16]byte{byte(i + 1)}, StartedAt: ts, EmployeeID: emp.ID,
			ClientKind: "codex", Endpoint: "responses", StatusCode: 200, TokensInput: 10, TokensOutput: 5}); err != nil {
			t.Fatal(err)
		}
	}
	first, ok, err := st.FirstRequestAt(ctx, at(0, 0), at(23, 0), []int64{emp.ID})
	if err != nil || !ok || !first.Equal(at(10, 2)) {
		t.Fatalf("first = %v %v %v", first, ok, err)
	}
	origin := time.Date(2000, 1, 1, 0, 0, 0, 0, loc)
	pts, err := st.UsageSeries(ctx, at(9, 50), at(12, 0), []int64{emp.ID}, 15*time.Minute, origin)
	if err != nil {
		t.Fatal(err)
	}
	// 09:45 … 11:45 on the local 15-minute grid, empty buckets included.
	if len(pts) != 9 || !pts[0].T.Equal(at(9, 45)) {
		t.Fatalf("got %d points starting %v", len(pts), pts[0].T)
	}
	byTime := map[string]int64{}
	for _, p := range pts {
		byTime[p.T.In(loc).Format("15:04")] = p.Requests
	}
	if byTime["10:00"] != 2 || byTime["11:30"] != 1 || byTime["10:45"] != 0 {
		t.Fatalf("buckets = %v", byTime)
	}
	if pts[1].Tokens != 30 {
		t.Fatalf("tokens = %d, want 30", pts[1].Tokens)
	}
}
