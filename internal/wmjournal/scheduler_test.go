package wmjournal

import (
	"context"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestSchedulerRespectsPauseAndDailyCutoff(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=false,from_day='2026-10-30' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	early := time.Date(2026, 10, 31, 1, 0, 0, 0, loc)
	if worked, err := ScheduleOne(ctx, st, nil, nil, early); err != nil || worked {
		t.Fatalf("paused %v %v", worked, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	if worked, err := ScheduleOne(ctx, st, nil, nil, early); err != nil || worked {
		t.Fatalf("cutoff %v %v", worked, err)
	}
	if worked, err := ScheduleOne(ctx, st, nil, nil, early.Add(3*time.Hour)); err != nil || !worked {
		t.Fatalf("creation %v %v", worked, err)
	}
	var day string
	if err := st.Pool.QueryRow(ctx, `SELECT day::text FROM wm_full_journal_runs`).Scan(&day); err != nil || day != "2026-10-30" {
		t.Fatalf("day %s %v", day, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_full_journal_runs SET state='failed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ScheduleOne(ctx, st, nil, nil, early.Add(3*time.Hour)); err == nil {
		t.Fatal("failed run did not stop schedule")
	}
}
