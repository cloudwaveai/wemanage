package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/timefmt"
)

// QuotaAlerter tells employees how their account's quota stands: once when a
// window (5 hours, weekly) reaches the threshold, and once when it is used
// up, each with when it resets and whether a reset credit (重置卡) could help.
// The message goes only to the employee, who can turn it off.
type QuotaAlerter struct {
	Store  *store.Store
	Sender interface {
		SendText(ctx context.Context, openID, text string) error
	}
	// Credits counts an account's reset credits; nil leaves them unmentioned.
	Credits interface {
		AvailableResetCredits(ctx context.Context, accountID int64) (int, error)
	}
	Loc       *time.Location
	PortalURL string
	Threshold float64          // percent; 80 when zero
	Now       func() time.Time // for tests
}

// usedUp is the share at which a window counts as used up.
const usedUp = 100

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
		a.check(ctx, t, "primary", "5 小时", q.PrimaryUsedPct, q.PrimaryResetAt, t.AlertedPrimary, t.ExhaustedPrimary, now)
		a.check(ctx, t, "secondary", "每周", q.SecondaryUsedPct, q.SecondaryResetAt, t.AlertedSecondary, t.ExhaustedSecondary, now)
	}
	return nil
}

// sameWindow: snapshots of one window give reset times a few seconds apart.
func sameWindow(told, reset *time.Time) bool {
	return told != nil && told.Sub(*reset).Abs() < 30*time.Minute
}

func (a *QuotaAlerter) check(ctx context.Context, t store.QuotaAlertTarget, window, label string,
	pct *float64, reset, alerted, exhausted *time.Time, now time.Time) {
	threshold := a.Threshold
	if threshold == 0 {
		threshold = 80
	}
	if pct == nil || reset == nil || *pct < threshold || !reset.After(now) {
		return
	}
	full := *pct >= usedUp
	if (full && sameWindow(exhausted, reset)) || (!full && sameWindow(alerted, reset)) {
		return
	}
	portal := strings.TrimRight(a.PortalURL, "/")
	when := timefmt.Relative(*reset, now, a.Loc)
	cards := a.credits(ctx, t.AccountID)
	var text string
	if full {
		text = fmt.Sprintf("【AI 网关】你的账号%s额度已用完，预计 %s 恢复。", label, when)
		if cards > 0 {
			text += fmt.Sprintf("\n你还有 %d 张重置卡，急用的话可以在门户「我的用量」里使用，5 小时和每周额度会立即重新开始：%s/usage", cards, portal)
		} else {
			text += "到时会自动恢复，期间可以先处理不需要 AI 的工作。"
		}
	} else {
		text = fmt.Sprintf("【AI 网关】你的账号%s额度已用 %.0f%%，预计 %s 重置。", label, *pct, when)
		if cards > 0 {
			text += fmt.Sprintf("你还有 %d 张重置卡，额度用完后可以在门户「我的用量」里使用。", cards)
		}
		text += fmt.Sprintf("\n不想收到这个提醒，可以在门户「我的用量」里关掉：%s/usage", portal)
	}
	if err := a.Sender.SendText(ctx, t.OpenID, text); err != nil {
		return // tried again on the next run
	}
	_ = a.Store.MarkQuotaAlerted(ctx, t.EmployeeID, window, full, *reset)
	if full {
		// Used up implies past the threshold: no 80% reminder after it.
		_ = a.Store.MarkQuotaAlerted(ctx, t.EmployeeID, window, false, *reset)
	}
}

func (a *QuotaAlerter) credits(ctx context.Context, accountID int64) int {
	if a.Credits == nil {
		return 0
	}
	n, err := a.Credits.AvailableResetCredits(ctx, accountID)
	if err != nil {
		return 0
	}
	return n
}
