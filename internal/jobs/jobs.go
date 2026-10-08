// Package jobs registers and runs the gateway's scheduled background tasks.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"aiseat/internal/feishu"
	"aiseat/internal/health"
	"aiseat/internal/notify"
	"aiseat/internal/report"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/robfig/cron/v3"
)

// Deps wires the scheduled jobs.
type Deps struct {
	Store         *store.Store
	Settings      *settings.Service
	Box           *secure.Box
	Notifier      *notify.Notifier
	Feishu        *feishu.Client
	Report        report.Deps
	EgressEchoURL string
	Loc           *time.Location
	GitHub        health.GitHub
	Risk          *risk.Engine        // optional
	Quota         *health.QuotaProber // optional
	QuotaAlerts   *QuotaAlerter       // optional
	Log           *slog.Logger
}

// Register attaches all scheduled jobs to c. The cron must use Deps.Loc.
func Register(c *cron.Cron, d Deps) {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	ctx := context.Background()

	// ChatGPT quota for idle accounts — every 5 minutes (only stale ones are probed).
	if d.Quota != nil {
		_, _ = c.AddFunc("@every 5m", func() {
			if err := d.Quota.RunStale(ctx); err != nil {
				log.Warn("quota probe", "err", err)
			}
		})
	}

	// ChatGPT subscriptions (next renewal) — checked every 6 hours per account.
	if d.Quota != nil {
		_, _ = c.AddFunc("@every 15m", func() {
			if err := d.Quota.RunSubscriptions(ctx); err != nil {
				log.Warn("subscription check", "err", err)
			}
		})
	}

	// Quota reminders employees asked for — every 2 minutes.
	if d.QuotaAlerts != nil {
		_, _ = c.AddFunc("@every 2m", func() {
			if err := d.QuotaAlerts.Run(ctx); err != nil {
				log.Warn("quota alerts", "err", err)
			}
		})
	}

	// Egress IP check — every 30 minutes.
	_, _ = c.AddFunc("@every 30m", func() {
		err := health.CheckAll(ctx, d.Store, d.Box, d.EgressEchoURL, 15*time.Second,
			func(groupID int64, oldIP, newIP string) {
				// Flag every account in the changed group.
				accs, _ := d.Store.ListAccounts(ctx)
				for _, a := range accs {
					if a.IPGroupID == groupID {
						_, _ = d.Store.AddSignal(ctx, a.ID, "egress_changed",
							map[string]any{"old": oldIP, "new": newIP})
					}
				}
			})
		if err != nil {
			log.Warn("egress check", "err", err)
		}
	})

	// Degradation sweep — every 15 minutes.
	_, _ = c.AddFunc("@every 15m", func() {
		set := d.Settings.Get(ctx)
		accs, _ := d.Store.ListAccounts(ctx)
		for _, a := range accs {
			if !a.IsChatGPT() {
				continue // the degradation heuristics are ChatGPT-specific
			}
			id := a.ID
			reasoning, _ := d.Store.RecentReasoning(ctx, id, 20)
			if err := health.CheckDegradation(ctx, d.Store, id, set.ModelMismatchThreshold, reasoning, time.Now()); err != nil {
				log.Warn("degradation check", "account", id, "err", err)
			}
		}
	})

	// Push unnotified account signals — every 5 minutes.
	_, _ = c.AddFunc("@every 5m", func() {
		if d.Notifier == nil {
			return
		}
		sigs, _ := d.Store.UnnotifiedSignals(ctx)
		for _, sig := range sigs {
			_ = d.Notifier.AccountAlert(ctx, sig.ID, sig.AccountID, sig.Kind, describeSignal(sig))
		}
	})

	// Feishu directory sync — hourly.
	if d.Feishu != nil {
		_, _ = c.AddFunc("@every 1h", func() {
			if _, _, err := feishu.Run(ctx, d.Store, d.Feishu); err != nil {
				log.Warn("feishu sync", "err", err)
			}
		})
	}

	// Codex version check — daily 09:15.
	_, _ = c.AddFunc("15 9 * * *", func() {
		latest, err := d.GitHub.Latest(ctx)
		if err != nil {
			return
		}
		set := d.Settings.Get(ctx)
		if latest != "" && latest != set.CodexProfile.Version {
			log.Info("newer Codex release available", "current", set.CodexProfile.Version, "latest", latest)
		}
	})

	// This week's report draft — Sundays 16:00, once the week's work is all in,
	// to each employee only.
	_, _ = c.AddFunc("0 16 * * 0", func() {
		thisWeek := report.WeekStart(time.Now(), d.Loc)
		if err := report.GenerateForWeek(ctx, d.Report, thisWeek); err != nil {
			log.Warn("weekly report", "err", err)
		}
	})

	if d.Risk != nil {
		// Pending devices past the confirmation window — hourly.
		_, _ = c.AddFunc("@every 1h", func() {
			if err := d.Risk.ExpirePending(ctx); err != nil {
				log.Warn("risk expire pending", "err", err)
			}
		})
	}

	// Retention cleanup — daily 03:30.
	_, _ = c.AddFunc("30 3 * * *", func() {
		set := d.Settings.Get(ctx)
		if err := Retention(ctx, d.Store, set.RetentionDays, time.Now()); err != nil {
			log.Warn("retention", "err", err)
		}
	})
}

func describeSignal(s store.Signal) string {
	switch s.Kind {
	case "rate_limited":
		return "账号触发限额"
	case "needs_login":
		return "账号需要重新登录"
	case "degraded":
		return "账号疑似被降智"
	case "egress_changed":
		return "账号出口 IP 发生变化"
	default:
		return "账号异常：" + s.Kind
	}
}
