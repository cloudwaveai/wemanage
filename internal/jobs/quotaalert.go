package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/timefmt"
)

// QuotaAlerter tells employees who asked for it when their account's quota
// reaches the threshold: once per window (5 hours, weekly), with when it
// resets. It is a courtesy the employee turns on; nobody else is told.
type QuotaAlerter struct {
	Store  *store.Store
	Sender interface {
		SendText(ctx context.Context, openID, text string) error
	}
	Loc       *time.Location
	PortalURL string
	Threshold float64          // percent; 80 when zero
	Now       func() time.Time // for tests
}

func (a *QuotaAlerter) Run(ctx context.Context) error {
	targets, err := a.Store.QuotaAlertTargets(ctx)
	if err != nil || len(targets) == 0 {
		return err
	}
	ids := make([]int64, len(targets))
	for i, t := range targets {
		ids[i] = t.AccountID
	}
	snaps, err := a.Store.LatestQuotas(ctx, ids)
	if err != nil {
		return err
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	for _, t := range targets {
		q, ok := snaps[t.AccountID]
		if !ok || t.OpenID == "" {
			continue
		}
		a.check(ctx, t, "primary", "5 小时", q.PrimaryUsedPct, q.PrimaryResetAt, t.AlertedPrimary, now)
		a.check(ctx, t, "secondary", "每周", q.SecondaryUsedPct, q.SecondaryResetAt, t.AlertedSecondary, now)
	}
	return nil
}

func (a *QuotaAlerter) check(ctx context.Context, t store.QuotaAlertTarget, window, label string,
	pct *float64, reset, alerted *time.Time, now time.Time) {
	threshold := a.Threshold
	if threshold == 0 {
		threshold = 80
	}
	if pct == nil || reset == nil || *pct < threshold || !reset.After(now) {
		return
	}
	// Snapshots of one window give reset times a few seconds apart.
	if alerted != nil && alerted.Sub(*reset).Abs() < 30*time.Minute {
		return
	}
	text := fmt.Sprintf("【AI 网关】你的账号%s额度已用 %.0f%%，预计 %s 重置。\n不想收到这个提醒，可以在门户「我的状态」里关掉：%s/me",
		label, *pct, timefmt.Relative(*reset, now, a.Loc), strings.TrimRight(a.PortalURL, "/"))
	if err := a.Sender.SendText(ctx, t.OpenID, text); err != nil {
		return // tried again on the next run
	}
	_ = a.Store.MarkQuotaAlerted(ctx, t.EmployeeID, window, *reset)
}
