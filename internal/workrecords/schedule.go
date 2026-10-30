package workrecords

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Tick creates missing daily work after 02:15 China time and advances a single
// run until the supplied deadline. Enabling is independent of the old digest
// flywheel. A failed or ambiguous run is left for inspection and newer days
// are not blocked by it.
func (q *Queue) Tick(ctx context.Context, model ModelExtractor, now time.Time) error {
	conn, err := q.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	var enabled bool
	var activated time.Time
	var sol string
	if err = conn.QueryRow(ctx, `SELECT enabled,activated_at,sol_model FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled, &activated, &sol); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	activation := activated.In(loc)
	start := time.Date(activation.Year(), activation.Month(), activation.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	last := today.AddDate(0, 0, -1)
	if local.Hour() < 2 || (local.Hour() == 2 && local.Minute() < 15) {
		last = last.AddDate(0, 0, -1)
	}
	for day := start; !day.After(last); day = day.AddDate(0, 0, 1) {
		var exists bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_runs WHERE day=$1::date)`, day).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = q.Start(ctx, day); err != nil {
				return err
			}
			break
		}
	}
	var run int64
	err = conn.QueryRow(ctx, `SELECT r.id FROM wm_full_journal_runs r WHERE NOT r.superseded AND r.state IN ('preparing','queued','running') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.state IN ('running','failed')) ORDER BY r.day,r.version LIMIT 1`).Scan(&run)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		// Do not begin a three-minute model call immediately before the cron
		// deadline; that would leave its upstream outcome unknown on timeout.
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 3*time.Minute+15*time.Second {
			return nil
		}
		var stillEnabled bool
		if err = conn.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&stillEnabled); err != nil {
			return err
		}
		if !stillEnabled {
			return nil
		}
		more, stepErr := q.Step(ctx, run, model, sol)
		if stepErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			message := "处理暂停，请检查任务状态"
			if errors.Is(stepErr, ErrEmbeddedMedia) {
				message = "处理暂停：记录含图片或音视频，需要先接入多模态处理；原始内容已保留"
			}
			_, _ = q.Store.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET state='failed',error=$2,updated_at=now() WHERE id=$1 AND state IN ('preparing','queued','running')`, run, message)
			return stepErr
		}
		if !more {
			return nil
		}
	}
	return ctx.Err()
}
