package store_test

import (
	"testing"
	"time"

	"aiseat/internal/store"
)

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
