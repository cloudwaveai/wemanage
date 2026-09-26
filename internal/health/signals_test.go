package health_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"aiseat/internal/health"
	"aiseat/internal/secure"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	pool := testutil.NewDB(t)
	return store.New(pool), context.Background()
}

func newBox(t *testing.T) *secure.Box {
	t.Helper()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func mkAccount(t *testing.T, st *store.Store, ctx context.Context, box *secure.Box, cpaName string) store.Account {
	t.Helper()
	// Group name must be unique (ip_groups.name has a unique constraint); derive
	// it from the per-account cpaName so multiple accounts in one test coexist.
	g, err := st.CreateIPGroup(ctx, box, "g-"+cpaName, "", 10)
	if err != nil {
		t.Fatalf("create ip group: %v", err)
	}
	a, err := st.CreateAccount(ctx, store.Account{
		CPAAuthName:   cpaName,
		IPGroupID:     g.ID,
		DeviceProfile: store.DeviceProfile{},
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return a
}

func TestEvaluateAccountPriority(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)
	a := mkAccount(t, st, ctx, box, "eval.json")

	// Baseline: active.
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusActive {
		t.Fatalf("active: got %q err %v", got, err)
	}

	// Degraded: open degraded signal (lowest non-active priority).
	if _, err := st.AddSignal(ctx, a.ID, "degraded", map[string]any{"why": "test"}); err != nil {
		t.Fatal(err)
	}
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusDegraded {
		t.Fatalf("degraded: got %q err %v", got, err)
	}

	// Rate-limited outranks degraded.
	until := time.Now().Add(time.Hour)
	if err := st.SetRateLimitedUntil(ctx, a.ID, &until); err != nil {
		t.Fatal(err)
	}
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusRateLimited {
		t.Fatalf("rate_limited: got %q err %v", got, err)
	}

	// A past deadline no longer counts as rate-limited -> falls back to degraded.
	past := time.Now().Add(-time.Hour)
	if err := st.SetRateLimitedUntil(ctx, a.ID, &past); err != nil {
		t.Fatal(err)
	}
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusDegraded {
		t.Fatalf("expired rate limit: got %q err %v", got, err)
	}

	// Needs-login outranks rate-limited.
	if err := st.SetRateLimitedUntil(ctx, a.ID, &until); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNeedsLogin(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusNeedsLogin {
		t.Fatalf("needs_login: got %q err %v", got, err)
	}

	// Disabled outranks everything.
	if err := st.UpdateAccount(ctx, a.ID, store.AccountPatch{Status: strptr("disabled")}); err != nil {
		t.Fatal(err)
	}
	if got, err := health.EvaluateAccount(ctx, st, a.ID); err != nil || got != health.StatusDisabled {
		t.Fatalf("disabled: got %q err %v", got, err)
	}
}

func strptr(s string) *string { return &s }

func TestRecordQuotaFromHeaders(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)
	a := mkAccount(t, st, ctx, box, "quota.json")

	now := time.Now().Truncate(time.Second)
	resetAt := now.Add(90 * time.Minute).Unix()

	h := http.Header{}
	h.Set("X-Codex-Primary-Used-Percent", "42.5")
	h.Set("X-Codex-Primary-Window-Minutes", "300")
	h.Set("X-Codex-Primary-Reset-At", strconv.FormatInt(resetAt, 10))

	if err := health.RecordQuotaFromHeaders(ctx, st, a.ID, h, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	q, err := st.LatestQuota(ctx, a.ID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if q.PrimaryUsedPct == nil || *q.PrimaryUsedPct < 42.4 || *q.PrimaryUsedPct > 42.6 {
		t.Fatalf("primary used pct = %v", q.PrimaryUsedPct)
	}
	if q.PrimaryWindowMinutes == nil || *q.PrimaryWindowMinutes != 300 {
		t.Fatalf("primary window minutes = %v", q.PrimaryWindowMinutes)
	}
	if q.PrimaryResetAt == nil || q.PrimaryResetAt.Unix() != resetAt {
		t.Fatalf("primary reset at = %v, want unix %d", q.PrimaryResetAt, resetAt)
	}
	if q.SecondaryUsedPct != nil {
		t.Fatalf("secondary used pct should be nil, got %v", q.SecondaryUsedPct)
	}

	// Reset-After-Seconds path computes now + seconds when Reset-At absent.
	h2 := http.Header{}
	h2.Set("X-Codex-Secondary-Used-Percent", "10")
	h2.Set("X-Codex-Secondary-Reset-After-Seconds", "600")
	now2 := now.Add(time.Hour)
	if err := health.RecordQuotaFromHeaders(ctx, st, a.ID, h2, now2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	q2, err := st.LatestQuota(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q2.SecondaryResetAt == nil || q2.SecondaryResetAt.Unix() != now2.Add(600*time.Second).Unix() {
		t.Fatalf("secondary reset at = %v", q2.SecondaryResetAt)
	}

	// Empty header set stores nothing (and returns nil).
	empty := mkAccount(t, st, ctx, box, "empty.json")
	if err := health.RecordQuotaFromHeaders(ctx, st, empty.ID, http.Header{}, now); err != nil {
		t.Fatalf("empty headers: %v", err)
	}
	if _, err := st.LatestQuota(ctx, empty.ID); err != store.ErrNotFound {
		t.Fatalf("expected no snapshot for empty headers, got %v", err)
	}
}

func TestCheckDegradation(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)
	now := time.Now()

	// Case 1: >= mismatchThreshold model_mismatch signals within 24h -> degraded.
	a := mkAccount(t, st, ctx, box, "mismatch.json")
	for i := 0; i < 3; i++ {
		if _, err := st.AddSignal(ctx, a.ID, "model_mismatch", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := health.CheckDegradation(ctx, st, a.ID, 3, nil, now); err != nil {
		t.Fatalf("check mismatch: %v", err)
	}
	if has, _ := st.HasOpenSignal(ctx, a.ID, "degraded"); !has {
		t.Fatal("expected degraded signal from mismatch threshold")
	}

	// Case 2: dominant low reasoning value (>=80% == 516, <=600) -> degraded.
	b := mkAccount(t, st, ctx, box, "reasoning.json")
	samples := []int64{516, 516, 516, 516, 516, 516, 516, 516, 516, 1024}
	if err := health.CheckDegradation(ctx, st, b.ID, 0, samples, now); err != nil {
		t.Fatalf("check reasoning: %v", err)
	}
	if has, _ := st.HasOpenSignal(ctx, b.ID, "degraded"); !has {
		t.Fatal("expected degraded signal from reasoning downgrade")
	}

	// Case 3: healthy (no mismatch, varied reasoning) resolves existing degraded.
	c := mkAccount(t, st, ctx, box, "healthy.json")
	if _, err := st.AddSignal(ctx, c.ID, "degraded", map[string]any{"seed": true}); err != nil {
		t.Fatal(err)
	}
	varied := []int64{1024, 2048, 512, 4096, 800}
	if err := health.CheckDegradation(ctx, st, c.ID, 3, varied, now); err != nil {
		t.Fatalf("check healthy: %v", err)
	}
	if has, _ := st.HasOpenSignal(ctx, c.ID, "degraded"); has {
		t.Fatal("expected degraded signal to be resolved for healthy account")
	}
}
