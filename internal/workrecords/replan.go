package workrecords

import (
	"context"
	"errors"
	"fmt"
)

// ReplanSnapshot preserves the old run and creates a new version from its
// immutable snapshot. It only accepts unexecuted, fully captured work while
// scheduling is paused. The transaction either publishes both version changes
// and every copied row, or publishes nothing.
func (q *Queue) ReplanSnapshot(ctx context.Context, old int64) (int64, error) {
	tx, err := q.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	for _, lock := range []int64{7419752105, 7419752104, 7419752103, 7419752102} {
		var ok bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lock).Scan(&ok); err != nil {
			return 0, err
		}
		if !ok {
			return 0, errors.New("journal is active")
		}
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1 FOR UPDATE`).Scan(&enabled); err != nil {
		return 0, err
	}
	if enabled {
		return 0, errors.New("pause journal first")
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT r.state='preparing' AND NOT r.superseded AND NOT r.planning_complete AND NOT EXISTS(SELECT 1 FROM wm_full_journal_sources s WHERE s.run_id=r.id AND s.status='pending') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id AND (b.state<>'queued' OR EXISTS(SELECT 1 FROM wm_full_journal_attempts a WHERE a.batch_id=b.id))) FROM wm_full_journal_runs r WHERE r.id=$1 FOR UPDATE`, old).Scan(&eligible)
	if err != nil {
		return 0, err
	}
	if !eligible {
		return 0, errors.New("snapshot is not safe to replan")
	}
	var run int64
	err = tx.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,version,state,previous_run_id,source_count,missing_count) SELECT day,(SELECT max(x.version)+1 FROM wm_full_journal_runs x WHERE x.day=r.day),'preparing',previous_run_id,source_count,missing_count FROM wm_full_journal_runs r WHERE id=$1 RETURNING id`, old).Scan(&run)
	if err != nil {
		return 0, err
	}
	statements := []string{
		`INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status,error) SELECT $2,request_id,employee_id,employee_name,session_id,started_at,endpoint,status,error FROM wm_full_journal_sources WHERE run_id=$1 ORDER BY id`,
		`INSERT INTO wm_full_journal_blobs(run_id,hash,kind,data) SELECT $2,hash,kind,data FROM wm_full_journal_blobs WHERE run_id=$1`,
		`INSERT INTO wm_full_journal_segments(source_id,direction,ordinal,hash,kind) SELECT n.id,g.direction,g.ordinal,g.hash,g.kind FROM wm_full_journal_segments g JOIN wm_full_journal_sources o ON o.id=g.source_id JOIN wm_full_journal_sources n ON n.run_id=$2 AND n.request_id=o.request_id WHERE o.run_id=$1`,
		`UPDATE wm_full_journal_runs SET superseded=true,state='failed',error='已保留原计划，改用新版本重新编排',updated_at=now() WHERE id=$1 AND $2>0`,
	}
	for i, sql := range statements {
		if _, err = tx.Exec(ctx, sql, old, run); err != nil {
			return 0, fmt.Errorf("replan stage %d: %w", i, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return run, nil
}
