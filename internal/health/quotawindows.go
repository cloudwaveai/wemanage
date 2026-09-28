package health

import (
	"time"

	"aiseat/internal/store"
)

// Window lengths (minutes) the portal labels "5 小时" and "每周".
const (
	fiveHourMinutes = 300
	weekMinutes     = 7 * 24 * 60
)

// NormalizeQuotaWindows puts the 5-hour (short) window in the primary slot
// and the weekly (long) one in the secondary slot. Upstreams report windows
// by position and the position is not fixed: a Pro plan can report its
// weekly window as "primary" with no secondary at all. A window is long when
// it is about a week (±5%, as the Codex CLI judges it) or anything over a
// day; a window of unknown length stays where it is.
func NormalizeQuotaWindows(q store.QuotaSnapshot) store.QuotaSnapshot {
	p := quotaWindow{q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt}
	s := quotaWindow{q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt}
	pLong, pKnown := p.isLong()
	sLong, sKnown := s.isLong()
	swap := false
	switch {
	case p.present() && s.present():
		swap = pKnown && sKnown && pLong && !sLong
	case p.present():
		swap = pKnown && pLong
	case s.present():
		swap = sKnown && !sLong
	}
	if swap {
		p, s = s, p
	}
	q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt = p.pct, p.minutes, p.resetAt
	q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt = s.pct, s.minutes, s.resetAt
	return q
}

type quotaWindow struct {
	pct     *float64
	minutes *int
	resetAt *time.Time
}

func (w quotaWindow) present() bool { return w.pct != nil || w.minutes != nil || w.resetAt != nil }

// isLong classifies the window by length; known is false without a length.
func (w quotaWindow) isLong() (long, known bool) {
	if w.minutes == nil {
		return false, false
	}
	m := *w.minutes
	switch {
	case near(m, fiveHourMinutes):
		return false, true
	case near(m, weekMinutes):
		return true, true
	default:
		return m > 24*60, true
	}
}

// near reports whether m is within 5% of want.
func near(m, want int) bool {
	return float64(m) >= 0.95*float64(want) && float64(m) <= 1.05*float64(want)
}
