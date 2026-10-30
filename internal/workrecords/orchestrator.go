package workrecords

import (
	"context"
	"errors"
	"fmt"
)

// Step performs one durable unit of work. It is safe to call repeatedly; it
// never retries a failed or ambiguous in-flight model call automatically.
// The lock also serializes planning, finalization and source snapshots.
func (q *Queue) Step(ctx context.Context, run int64, model ModelExtractor, solModel string) (bool, error) {
	conn, err := q.Store.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var lock bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752104)`).Scan(&lock); err != nil {
		return false, err
	}
	if !lock {
		return false, errors.New("journal task is already running")
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752104)`)
	var state string
	if err = conn.QueryRow(ctx, `SELECT state FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&state); err != nil {
		return false, err
	}
	if state == "done" || state == "partial" {
		return false, nil
	}
	if state == "failed" {
		return false, errors.New("journal needs attention")
	}
	if state == "preparing" {
		more, err := q.SnapshotNext(ctx, run)
		if err != nil {
			return false, err
		}
		if more {
			return true, nil
		}
		more, err = q.PlanNext(ctx, run, 100000)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	var phase string
	var level, queued, unfinished int
	err = conn.QueryRow(ctx, `SELECT phase,level,count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state IN ('running','failed')) FROM wm_full_journal_batches WHERE run_id=$1 GROUP BY phase,level ORDER BY CASE phase WHEN 'synthesis' THEN 4 WHEN 'daily' THEN 3 WHEN 'reduce' THEN 2 ELSE 1 END DESC,level DESC LIMIT 1`, run).Scan(&phase, &level, &queued, &unfinished)
	if err != nil {
		return false, fmt.Errorf("no available journal batches: %w", err)
	}
	if unfinished > 0 {
		return false, errors.New("batch failed or outcome unknown; operator review required")
	}
	if queued > 0 {
		if phase == "extract" {
			return q.ProcessNext(ctx, run, model)
		}
		return q.ProcessReduction(ctx, run, phase, level, model, solModel)
	}
	switch phase {
	case "extract", "reduce":
		_, _, err = q.PrepareReduction(ctx, run, phase, level, 100000)
		return err == nil, err
	case "daily":
		err = q.PrepareSynthesis(ctx, run, 100000)
		return err == nil, err
	case "synthesis":
		err = q.Finish(ctx, run)
		return false, err
	default:
		return false, errors.New("unknown journal phase")
	}
}
