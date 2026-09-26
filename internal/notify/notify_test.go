package notify_test

import (
	"context"
	"sync"
	"testing"

	"aiseat/internal/notify"
	"aiseat/internal/secure"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

// fakeSender records every SendText call.
type fakeSender struct {
	mu    sync.Mutex
	calls []call
}

type call struct{ openID, text string }

func (f *fakeSender) SendText(ctx context.Context, openID, text string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call{openID, text})
	f.mu.Unlock()
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestAccountAlertSendsMarksAndDedups(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()

	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	g, err := st.CreateIPGroup(ctx, box, "g", "", 10)
	if err != nil {
		t.Fatalf("create ip group: %v", err)
	}
	a, err := st.CreateAccount(ctx, store.Account{
		CPAAuthName:   "c.json",
		IPGroupID:     g.ID,
		DeviceProfile: store.DeviceProfile{},
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	sig, err := st.AddSignal(ctx, a.ID, "rate_limited", nil)
	if err != nil {
		t.Fatalf("add signal: %v", err)
	}
	if sig.NotifiedAt != nil {
		t.Fatalf("new signal already notified: %+v", sig.NotifiedAt)
	}

	sender := &fakeSender{}
	admins := []string{"ou_admin1", "ou_admin2"}
	n := notify.NewNotifier(st, sender, admins)

	// First alert: sends to every super admin.
	if err := n.AccountAlert(ctx, sig.ID, a.ID, "rate_limited", "429 from upstream"); err != nil {
		t.Fatalf("first alert: %v", err)
	}
	if got := sender.count(); got != len(admins) {
		t.Fatalf("first alert sent %d messages, want %d", got, len(admins))
	}
	f := sender
	f.mu.Lock()
	seen := map[string]bool{}
	for _, c := range f.calls {
		seen[c.openID] = true
	}
	f.mu.Unlock()
	for _, ad := range admins {
		if !seen[ad] {
			t.Fatalf("admin %q was not notified", ad)
		}
	}

	// The signal is now marked notified in the store.
	open, err := st.OpenSignals(ctx, a.ID)
	if err != nil {
		t.Fatalf("open signals: %v", err)
	}
	if len(open) != 1 || open[0].NotifiedAt == nil {
		t.Fatalf("signal notified_at not set: %+v", open)
	}

	// And it is no longer among the unnotified set.
	un, err := st.UnnotifiedSignals(ctx)
	if err != nil {
		t.Fatalf("unnotified: %v", err)
	}
	for _, s := range un {
		if s.ID == sig.ID {
			t.Fatalf("signal %d still unnotified after alert", sig.ID)
		}
	}

	// Second identical alert within 24h: suppressed, no new sends.
	if err := n.AccountAlert(ctx, sig.ID, a.ID, "rate_limited", "429 again"); err != nil {
		t.Fatalf("second alert: %v", err)
	}
	if got := sender.count(); got != len(admins) {
		t.Fatalf("after dedup, total sent = %d, want unchanged %d", got, len(admins))
	}

	// A different kind for the same account is NOT deduped.
	sig2, err := st.AddSignal(ctx, a.ID, "needs_login", nil)
	if err != nil {
		t.Fatalf("add second signal: %v", err)
	}
	if err := n.AccountAlert(ctx, sig2.ID, a.ID, "needs_login", "session expired"); err != nil {
		t.Fatalf("third alert: %v", err)
	}
	if got := sender.count(); got != 2*len(admins) {
		t.Fatalf("distinct-kind alert total sent = %d, want %d", got, 2*len(admins))
	}
}
