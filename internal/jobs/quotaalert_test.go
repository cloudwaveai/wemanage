package jobs_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/jobs"
	"aiseat/internal/secure"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

type sent struct {
	mu   sync.Mutex
	msgs []string
}

func (s *sent) SendText(_ context.Context, _ string, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, text)
	return nil
}

func TestQuotaAlertOncePerWindow(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_q", Name: "q"})
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	acc, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "a.json", IPGroupID: g.ID})
	_ = st.BindAccount(ctx, acc.ID, emp.ID)

	now := time.Now()
	pct := func(v float64) *float64 { return &v }
	snap := func(p float64, reset time.Time) {
		t.Helper()
		if err := st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: acc.ID, At: time.Now(), PrimaryUsedPct: pct(p), PrimaryResetAt: &reset}); err != nil {
			t.Fatal(err)
		}
	}
	out := &sent{}
	a := &jobs.QuotaAlerter{Store: st, Sender: out, PortalURL: "https://portal", Now: func() time.Time { return now }}
	run := func() int {
		t.Helper()
		if err := a.Run(ctx); err != nil {
			t.Fatal(err)
		}
		out.mu.Lock()
		defer out.mu.Unlock()
		return len(out.msgs)
	}

	reset := now.Add(time.Hour)
	snap(90, reset)
	if n := run(); n != 0 {
		t.Fatalf("reminder sent without opting in: %d", n)
	}
	if err := st.SetQuotaAlert(ctx, emp.ID, true); err != nil {
		t.Fatal(err)
	}
	snap(50, reset)
	if n := run(); n != 0 {
		t.Fatalf("reminder below 80%%: %d", n)
	}
	snap(85, reset)
	if n := run(); n != 1 || !strings.Contains(out.msgs[0], "5 小时额度已用 85%") || !strings.Contains(out.msgs[0], "https://portal/usage") {
		t.Fatalf("first reminder: %q", out.msgs)
	}
	snap(95, reset.Add(3*time.Second)) // same window, reset time a little off
	if n := run(); n != 1 {
		t.Fatalf("second reminder in the same window: %d", n)
	}
	snap(82, reset.Add(5*time.Hour)) // next window
	if n := run(); n != 2 {
		t.Fatalf("next window not reminded: %d", n)
	}
}
