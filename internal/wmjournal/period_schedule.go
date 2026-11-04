package wmjournal

import (
	"context"
	"errors"
	"sort"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/workrecords"
	"github.com/jackc/pgx/v5"
)

func SchedulePeriod(ctx context.Context, st *store.Store, from, last time.Time, model workrecords.ModelExtractor, sol string) (bool, error) {
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var lock, enabled, old bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(7419752105)`).Scan(&lock); err != nil || !lock {
		return false, nil
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7419752105)`)
	if err = conn.QueryRow(ctx, `SELECT c.enabled,s.enabled FROM wm_log_worker_control c CROSS JOIN wm_full_journal_settings s WHERE c.id=1 AND s.id=1`).Scan(&enabled, &old); err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	if old {
		return false, errors.New("旧整理程序仍启用")
	}
	var id int64
	var state string
	err = conn.QueryRow(ctx, `SELECT id,state FROM wm_log_period_runs WHERE state IN ('planning','running','failed') ORDER BY period_end,kind LIMIT 1`).Scan(&id, &state)
	if err == nil {
		if state == "failed" {
			return false, errors.New("周期复盘需要检查")
		}
		if state == "planning" {
			return false, errors.New("周期复盘计划未完成")
		}
		return AdvancePeriod(ctx, st, id, model, sol)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return false, err
	}
	first := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	endLimit := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	type candidate struct {
		kind       string
		start, end time.Time
	}
	var candidates []candidate
	m := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, loc)
	for end := m.AddDate(0, 1, 0); !end.After(endLimit); end = m.AddDate(0, 1, 0) {
		candidates = append(candidates, candidate{"monthly", m, end})
		m = end
	}
	qmonth := time.Month(((int(first.Month())-1)/3)*3 + 1)
	q := time.Date(first.Year(), qmonth, 1, 0, 0, 0, 0, loc)
	for end := q.AddDate(0, 3, 0); !end.After(endLimit); end = q.AddDate(0, 3, 0) {
		candidates = append(candidates, candidate{"quarterly", q, end})
		q = end
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].end.Equal(candidates[j].end) {
			return candidates[i].kind < candidates[j].kind
		}
		return candidates[i].end.Before(candidates[j].end)
	})
	for _, c := range candidates {
		var exists bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_log_period_runs WHERE kind=$1 AND period_start=$2::date)`, c.kind, c.start).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			continue
		}
		_, err = CreatePeriod(ctx, st, c.kind, c.start, c.end, first)
		return err == nil, err
	}
	return false, nil
}
