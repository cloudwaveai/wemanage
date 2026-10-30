package workrecords

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var errEvidenceInterrupted = errors.New("evidence planning interrupted")

// PlanEvidenceRun writes a complete deterministic plan in one transaction.
// Failure or cancellation rolls back every batch, so retry starts from the
// preserved snapshot without duplicate calls or half-published plans.
// The caller must hold the scheduler advisory lock.
func (q *Queue) PlanEvidenceRun(ctx context.Context, run int64, expectEnabled bool, progress func(int64)) error {
	tx, err := q.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT r.state='preparing' AND NOT r.superseded AND NOT r.planning_complete AND r.plan_format=$2 AND NOT EXISTS(SELECT 1 FROM wm_full_journal_sources s WHERE s.run_id=r.id AND s.status='pending') AND NOT EXISTS(SELECT 1 FROM wm_full_journal_batches b WHERE b.run_id=r.id) FROM wm_full_journal_runs r WHERE r.id=$1 FOR UPDATE`, run, evidenceFormat).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return errors.New("evidence snapshot is not ready")
	}
	var incomplete bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_sources s WHERE s.run_id=$1 AND s.status='ready' AND NOT EXISTS(SELECT 1 FROM wm_full_journal_segments g WHERE g.source_id=s.id))`, run).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete {
		return errors.New("ready source has no snapshot segments")
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errors.New("source privacy changed")
	}
	checkSettings := func() error {
		var enabled bool
		if e := q.Store.Pool.QueryRow(ctx, `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&enabled); e != nil {
			return e
		}
		if enabled != expectEnabled {
			return errEvidenceInterrupted
		}
		return ctx.Err()
	}
	if err = checkSettings(); err != nil {
		return err
	}
	ordinal := 0
	planner, err := NewEvidencePlanner(100000, func(batch EvidenceBatch) error {
		encoded, e := json.Marshal(batch)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state) VALUES($1,'extract',$2,$3,'queued')`, run, ordinal, encoded); e != nil {
			return e
		}
		ordinal++
		return nil
	})
	if err != nil {
		return err
	}
	groups := int64(0)
	err = q.walkEvidence(ctx, run, func(group EvidenceGroup) error {
		groups++
		if groups%250 == 0 {
			if e := checkSettings(); e != nil {
				return e
			}
		}
		if e := planner.Add(group); e != nil {
			return e
		}
		if progress != nil && groups%1000 == 0 {
			progress(groups)
		}
		return nil
	}, nil)
	if err != nil {
		return err
	}
	if err = planner.Finish(); err != nil {
		return err
	}
	if err = checkSettings(); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, run).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return errors.New("source privacy changed")
	}
	// Snapshot the gateway's existing daily reports as navigational context.
	// Only identities already present in this run and still non-private are eligible.
	if _, err = tx.Exec(ctx, `INSERT INTO wm_full_journal_daily_context(run_id,employee_id,employee_name,summary_md,digest_model,digest_generated_at) SELECT $1,w.employee_id,e.name,w.summary_md,w.model,w.generated_at FROM wm_full_journal_runs r JOIN work_digests w ON w.day=r.day JOIN work_digest_days d ON d.day=w.day AND d.status='done' JOIN employees e ON e.id=w.employee_id WHERE r.id=$1 AND e.private_work_since IS NULL AND e.name<>'林伊默' AND EXISTS(SELECT 1 FROM wm_full_journal_sources s WHERE s.run_id=r.id AND s.employee_id=e.id)`, run); err != nil {
		return err
	}
	if ordinal == 0 {
		_, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET state='partial',planning_complete=true,error='当日没有可完整读取的记录；这不代表没有工作',updated_at=now() WHERE id=$1`, run)
	} else {
		if _, err = tx.Exec(ctx, `UPDATE wm_full_journal_sources SET planned=true WHERE run_id=$1 AND status='ready'`, run); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE wm_full_journal_runs SET state='queued',planning_complete=true,updated_at=now() WHERE id=$1`, run)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit evidence plan: %w", err)
	}
	return nil
}
