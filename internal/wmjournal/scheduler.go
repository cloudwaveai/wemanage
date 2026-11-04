package wmjournal

import (
	"context"
	"errors"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/workrecords"
	"github.com/jackc/pgx/v5"
)

// ScheduleOne advances at most one snapshot, extraction, or reduction step.
// This independent control never enables the old scheduler.
func ScheduleOne(ctx context.Context, st *store.Store, extract Extractor, model workrecords.ModelExtractor, now time.Time) (bool, error) {
	var enabled, oldEnabled bool
	var from time.Time
	var sol string
	if err := st.Pool.QueryRow(ctx, `SELECT c.enabled,c.from_day,s.enabled,s.sol_model FROM wm_log_worker_control c CROSS JOIN wm_full_journal_settings s WHERE c.id=1 AND s.id=1`).Scan(&enabled, &from, &oldEnabled, &sol); err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	if oldEnabled {
		return false, errors.New("旧整理程序仍启用")
	}
	var run int64
	var state string
	err := st.Pool.QueryRow(ctx, `SELECT id,state FROM wm_full_journal_runs WHERE NOT superseded AND state IN ('preparing','queued','running','failed') ORDER BY day,version LIMIT 1`).Scan(&run, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		var blocked bool
		if e := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_log_knowledge_runs k JOIN wm_full_journal_runs r ON r.id=k.journal_run_id WHERE NOT r.superseded AND k.state IN ('running','failed'))`).Scan(&blocked); e != nil {
			return false, e
		}
		if blocked {
			return false, errors.New("知识候选任务需要检查")
		}
		var completed int64
		e := st.Pool.QueryRow(ctx, `SELECT r.id FROM wm_full_journal_runs r LEFT JOIN wm_log_knowledge_runs k ON k.journal_run_id=r.id WHERE NOT r.superseded AND r.state IN ('done','partial') AND r.synthesis<>'' AND (k.id IS NULL OR k.state='queued') ORDER BY r.day,r.version LIMIT 1`).Scan(&completed)
		if e == nil {
			e = ExtractKnowledge(ctx, st, completed, model, sol)
			return e == nil, e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return false, e
		}
		loc, e := time.LoadLocation("Asia/Shanghai")
		if e != nil {
			return false, e
		}
		today := now.In(loc)
		last := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
		if today.Hour() < 2 || (today.Hour() == 2 && today.Minute() < 15) {
			last = last.AddDate(0, 0, -1)
		}
		var day time.Time
		e = st.Pool.QueryRow(ctx, `SELECT d::date FROM generate_series($1::date,$2::date,interval '1 day') d WHERE NOT EXISTS(SELECT 1 FROM wm_full_journal_runs r WHERE r.day=d::date AND NOT r.superseded) ORDER BY d LIMIT 1`, from, last).Scan(&day)
		if errors.Is(e, pgx.ErrNoRows) {
			return SchedulePeriod(ctx, st, from, last, model, sol)
		}
		if e != nil {
			return false, e
		}
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		conn, e := st.Pool.Acquire(ctx)
		if e != nil {
			return false, e
		}
		defer conn.Release()
		var locked, stillEnabled, oldStillEnabled, exists bool
		if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&locked); e != nil || !locked {
			return false, e
		}
		defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
		if e = conn.QueryRow(ctx, `SELECT c.enabled,s.enabled,EXISTS(SELECT 1 FROM wm_full_journal_runs r WHERE r.day=$1::date AND NOT r.superseded) FROM wm_log_worker_control c CROSS JOIN wm_full_journal_settings s WHERE c.id=1 AND s.id=1`, day).Scan(&stillEnabled, &oldStillEnabled, &exists); e != nil {
			return false, e
		}
		if !stillEnabled || oldStillEnabled || exists {
			return false, nil
		}
		_, e = (&workrecords.Queue{Store: st}).Start(ctx, day)
		return e == nil, e
	}
	if err != nil {
		return false, err
	}
	if state == "failed" {
		return false, errors.New("任务需要检查")
	}
	if state == "preparing" {
		return prepareOne(ctx, st, run)
	}
	var phase string
	var queued int
	if err = st.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT b.phase FROM wm_full_journal_batches b WHERE b.run_id=$1 ORDER BY CASE b.phase WHEN 'synthesis' THEN 4 WHEN 'daily' THEN 3 WHEN 'reduce' THEN 2 ELSE 1 END DESC,b.level DESC LIMIT 1),''),(SELECT count(*) FROM wm_full_journal_batches WHERE run_id=$1 AND phase='extract' AND state='queued')`, run).Scan(&phase, &queued); err != nil {
		return false, err
	}
	if phase == "extract" && queued > 0 {
		return ProcessPair(ctx, st.Pool, run, extract)
	}
	return AdvanceReduction(ctx, st, run, model, sol)
}

func prepareOne(ctx context.Context, st *store.Store, run int64) (bool, error) {
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var lock, oldEnabled, enabled bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&lock); err != nil || !lock {
		return false, nil
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	if err = conn.QueryRow(ctx, `SELECT c.enabled,s.enabled FROM wm_log_worker_control c CROSS JOIN wm_full_journal_settings s WHERE c.id=1 AND s.id=1`).Scan(&enabled, &oldEnabled); err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	if oldEnabled {
		return false, errors.New("旧整理程序仍启用")
	}
	q := &workrecords.Queue{Store: st}
	more, err := q.SnapshotNext(ctx, run)
	if err != nil || more {
		return more, err
	}
	if err = q.PlanEvidenceRun(ctx, run, false, nil); err != nil {
		return false, err
	}
	return true, nil
}
