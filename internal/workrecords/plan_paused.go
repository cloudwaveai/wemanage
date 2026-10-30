package workrecords

import (
	"context"
	"errors"
)

// PlanPaused prepares batches while scheduling is disabled, without access to
// a model client. The scheduler lock prevents another worker advancing the run.
func (q *Queue) PlanPaused(ctx context.Context, run int64, progress func(int)) error {
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
		return errors.New("scheduler active")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	var format string
	if err = conn.QueryRow(ctx, `SELECT plan_format FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&format); err != nil {
		return err
	}
	if format == evidenceFormat {
		return q.PlanEvidenceRun(ctx, run, false, func(n int64) {
			if progress != nil {
				progress(int(n))
			}
		})
	}
	for i := 0; ; i++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		var allowed bool
		if err = conn.QueryRow(ctx, `SELECT NOT s.enabled AND NOT r.superseded AND r.state='preparing' FROM wm_full_journal_settings s JOIN wm_full_journal_runs r ON r.id=$1 WHERE s.id=1`, run).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("planning interrupted or run unavailable")
		}
		more, e := q.PlanNext(ctx, run, 100000)
		if e != nil {
			return e
		}
		if progress != nil && (i%100 == 0 || !more) {
			progress(i)
		}
		if !more {
			return nil
		}
	}
}
