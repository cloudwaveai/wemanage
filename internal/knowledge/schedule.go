package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *Runner) location() *time.Location {
	if r.Loc != nil {
		return r.Loc
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return loc
}
func (r *Runner) Day(t time.Time) time.Time {
	t = t.In(r.location())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, r.location())
}
func (r *Runner) Week(t time.Time) time.Time {
	day := r.Day(t)
	n := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -n)
}

// Tick catches up one day and one week per pass. It starts with the day before
// activation; it never silently drops older missed, ready periods.
func (r *Runner) Tick(ctx context.Context, now time.Time) error {
	var enabled bool
	var activated time.Time
	if err := r.Store.Pool.QueryRow(ctx, `SELECT enabled,activated_at FROM wm_knowledge_schedule WHERE id=1`).Scan(&enabled, &activated); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	today := r.Day(now)
	local := now.In(r.location())
	eligible := today
	if local.Hour() < 2 || (local.Hour() == 2 && local.Minute() < 15) {
		eligible = today.AddDate(0, 0, -1)
	}
	start := r.Day(activated).AddDate(0, 0, -1)
	var day time.Time
	err := r.Store.Pool.QueryRow(ctx, `SELECT d.day FROM work_digest_days d LEFT JOIN LATERAL (SELECT z.* FROM wm_knowledge_runs z WHERE z.kind='daily' AND z.period_start=d.day AND z.period_end=d.day+1 ORDER BY z.version DESC LIMIT 1) run ON true WHERE d.day >= $1::date AND d.day < $2::date AND d.status='done' AND (run.id IS NULL OR (run.status='failed' AND run.finished_at < now()-interval '2 hours' AND (SELECT COUNT(*) FROM wm_knowledge_attempts a WHERE a.run_id=run.id)<3) OR (run.status='running' AND run.started_at < now()-interval '30 minutes') OR (run.status IN ('done','empty') AND EXISTS (SELECT 1 FROM work_digests wd JOIN employees e ON e.id=wd.employee_id LEFT JOIN wm_knowledge_sources src ON src.run_id=run.id AND src.source_day=wd.day AND src.employee_id=wd.employee_id WHERE wd.day=d.day AND e.private_work_since IS NULL AND e.name <> '林伊默' AND (src.id IS NULL OR wd.generated_at > src.digest_generated_at)))) ORDER BY d.day LIMIT 1`, start, eligible).Scan(&day)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		d := r.Day(day)
		if err := r.Run(ctx, "daily", d, d.AddDate(0, 0, 1)); err != nil {
			return err
		}
	}
	// Weeks begin Monday. Only complete weeks at/after activation are queued.
	week := r.Week(activated)
	for end := week.AddDate(0, 0, 7); !end.After(today); week, end = week.AddDate(0, 0, 7), end.AddDate(0, 0, 7) {
		if end.Equal(today) && (local.Hour() < 3 || (local.Hour() == 3 && local.Minute() < 15)) {
			break
		}
		var ready int
		if err := r.Store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM work_digest_days WHERE day >= $1::date AND day < $2::date AND status='done'`, week, end).Scan(&ready); err != nil {
			return err
		}
		if ready != 7 {
			continue
		}
		var status string
		var runID int64
		var retry bool
		err := r.Store.Pool.QueryRow(ctx, `SELECT id,status,(status='failed' AND finished_at < now()-interval '2 hours' AND (SELECT COUNT(*) FROM wm_knowledge_attempts a WHERE a.run_id=wm_knowledge_runs.id)<3) OR (status='running' AND started_at < now()-interval '30 minutes') FROM wm_knowledge_runs WHERE kind='weekly' AND period_start=$1::date AND period_end=$2::date ORDER BY version DESC LIMIT 1`, week, end).Scan(&runID, &status, &retry)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) || retry {
			return r.Run(ctx, "weekly", week, end)
		}
		if status == "done" || status == "empty" {
			var changed bool
			err = r.Store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM work_digests wd JOIN employees e ON e.id=wd.employee_id LEFT JOIN wm_knowledge_sources src ON src.run_id=$1 AND src.source_day=wd.day AND src.employee_id=wd.employee_id WHERE wd.day >= $2::date AND wd.day < $3::date AND e.private_work_since IS NULL AND e.name <> '林伊默' AND (src.id IS NULL OR wd.generated_at > src.digest_generated_at))`, runID, week, end).Scan(&changed)
			if err != nil {
				return err
			}
			if changed {
				return r.Run(ctx, "weekly", week, end)
			}
		}
	}
	return nil
}
