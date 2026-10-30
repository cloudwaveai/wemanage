package workrecords

import (
	"context"
	"errors"
)

// ProcessOnePaused runs only the first extract batch of a fresh, paused run.
// It cannot retry or silently repeat an earlier/unknown model attempt.
func (q *Queue) ProcessOnePaused(ctx context.Context, run int64, model Extractor) (bool, error) {
	conn, err := q.Store.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, errors.New("journal scheduler active")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	var allowed bool
	err = conn.QueryRow(ctx, `SELECT NOT s.enabled AND r.state='queued' AND NOT r.superseded AND r.plan_format=$2 AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id AND b.state<>'queued') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_attempts a JOIN wm_full_journal_batches b ON b.id=a.batch_id WHERE b.run_id=r.id) FROM wm_full_journal_runs r CROSS JOIN wm_full_journal_settings s WHERE r.id=$1 AND s.id=1`, run, evidenceFormat).Scan(&allowed)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, errors.New("sample requires a fresh paused plan")
	}
	return q.ProcessNext(ctx, run, model)
}
