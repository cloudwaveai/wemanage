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

type twoCards struct{}

func (twoCards) AvailableResetCredits(context.Context, int64) (int, error) { return 2, nil }

func TestQuotaAlertOncePerWindow(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_q", Name: "q"})
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	acc, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "a.json", IPGroupID: g.ID})
	_ = st.BindAccount(ctx, acc.ID, emp.ID, 3)

	now := time.Now()
	pct := func(v float64) *float64 { return &v }
	snap := func(p float64, reset time.Time) {
		t.Helper()
		if err := st.InsertQuota(ctx, store.QuotaSnapshot{AccountID: acc.ID, At: time.Now(), PrimaryUsedPct: pct(p), PrimaryResetAt: &reset}); err != nil {
			t.Fatal(err)
		}
	}
	out := &sent{}
	a := &jobs.QuotaAlerter{Store: st, Sender: out, Credits: twoCards{}, PortalURL: "https://portal", Now: func() time.Time { return now }}
	run := func() []string {
		t.Helper()
		if err := a.Run(ctx); err != nil {
			t.Fatal(err)
		}
		out.mu.Lock()
		defer out.mu.Unlock()
		return append([]string(nil), out.msgs...)
	}

	reset := now.Add(time.Hour)
	// Off: nothing, however high.
	if err := st.SetQuotaAlert(ctx, emp.ID, false); err != nil {
		t.Fatal(err)
	}
	snap(90, reset)
	if n := len(run()); n != 0 {
		t.Fatalf("reminder sent while turned off: %d", n)
	}
	// On by default for everyone else; back on here.
	if e, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_new", Name: "new"}); !e.QuotaAlert {
		t.Fatal("the reminder should be on for a new employee")
	}
	if err := st.SetQuotaAlert(ctx, emp.ID, true); err != nil {
		t.Fatal(err)
	}
	snap(50, reset)
	if n := len(run()); n != 0 {
		t.Fatalf("reminder below 80%%: %d", n)
	}
	snap(85, reset)
	msgs := run()
	// Reset credits are a last resort: the 80% reminder does not bring them up.
	if len(msgs) != 1 || !strings.Contains(msgs[0], "5 小时额度已用 85%") || strings.Contains(msgs[0], "重置卡") ||
		!strings.Contains(msgs[0], "https://portal/usage") {
		t.Fatalf("80%% reminder: %q", msgs)
	}
	snap(95, reset.Add(3*time.Second)) // same window, reset time a little off
	if n := len(run()); n != 1 {
		t.Fatalf("second 80%% reminder in the same window: %d", n)
	}
	snap(100, reset)
	msgs = run()
	if len(msgs) != 2 || !strings.Contains(msgs[1], "5 小时额度已用完") || !strings.Contains(msgs[1], "也可以现在用重置卡") ||
		!strings.Contains(msgs[1], "还有 2 张") {
		t.Fatalf("used-up message: %q", msgs)
	}
	if n := len(run()); n != 2 {
		t.Fatalf("used-up message repeated: %d", n)
	}
	// Next window straight to used up: one message, no 80% one after it.
	next := reset.Add(5 * time.Hour)
	snap(100, next)
	run()
	snap(90, next)
	if msgs = run(); len(msgs) != 3 || !strings.Contains(msgs[2], "已用完") {
		t.Fatalf("next window: %q", msgs)
	}
}
