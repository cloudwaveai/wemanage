package workrecords

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PrepareAvailable runs only source snapshots and batch planning. It holds
// the same scheduler lock as the gateway so the two processes cannot race;
// after preparation is complete it releases the lock for model processing.
// This worker has no model client and cannot trigger model calls.
func (q *Queue) PrepareAvailable(ctx context.Context) (int64, int, error) {
	conn, err := q.Store.Pool.Acquire(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil {
		return 0, 0, err
	}
	if !locked {
		return 0, 0, nil
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	var enabled bool
	if err = conn.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled); err != nil {
		return 0, 0, err
	}
	if !enabled {
		return 0, 0, nil
	}
	var run int64
	err = conn.QueryRow(ctx, `SELECT id FROM wm_full_journal_runs WHERE state='preparing' AND NOT superseded ORDER BY day,version LIMIT 1`).Scan(&run)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	steps := 0
	for ctx.Err() == nil {
		var state string
		if err = conn.QueryRow(ctx, `SELECT r.state,s.enabled FROM wm_full_journal_runs r CROSS JOIN wm_full_journal_settings s WHERE r.id=$1 AND s.id=1`, run).Scan(&state, &enabled); err != nil {
			return run, steps, err
		}
		if !enabled || state != "preparing" {
			return run, steps, nil
		}
		more, stepErr := q.Step(ctx, run, nil, "")
		if stepErr != nil {
			if ctx.Err() != nil {
				return run, steps, ctx.Err()
			}
			// Match the gateway's fail-closed behavior; keep all completed work.
			_, _ = q.Store.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET state='failed',error='处理暂停，请检查任务状态',updated_at=now() WHERE id=$1 AND state='preparing'`, run)
			return run, steps, stepErr
		}
		if !more {
			return run, steps, nil
		}
		steps++
		// Yield between records so the shared database remains responsive to
		// the gateway and the workbench while large days are prepared.
		timer := time.NewTimer(40 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return run, steps, ctx.Err()
		case <-timer.C:
		}
	}
	return run, steps, ctx.Err()
}
