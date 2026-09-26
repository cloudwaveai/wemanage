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
	Log           *slog.Logger
}

// Register attaches all scheduled jobs to c. The cron must use Deps.Loc.
func Register(c *cron.Cron, d Deps) {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	ctx := context.Background()

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
		ids, _ := d.Store.AllAccountIDs(ctx)
		for _, id := range ids {
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

	// AI daily report — generate + push at 08:30 for the previous day.
	_, _ = c.AddFunc("30 8 * * *", func() {
		yesterday := time.Now().In(d.Loc).AddDate(0, 0, -1)
		day := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, d.Loc)
		if err := report.GenerateForDate(ctx, d.Report, day); err != nil {
			log.Warn("daily report", "err", err)
		}
	})

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
