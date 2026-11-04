package wmjournal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
	"aiseat/internal/workrecords"
)

func TestMonthlyReviewKeepsCoverageAndUsesSol(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	e, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "period-member", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	from := time.Date(2026, 10, 29, 0, 0, 0, 0, loc)
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	for i := 0; i < 3; i++ {
		day := from.AddDate(0, 0, i)
		var run int64
		if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,daily_report,synthesis) VALUES($1,'done','日报内容','知识总结') RETURNING id`, day).Scan(&run); err != nil {
			t.Fatal(err)
		}
		if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,gen_random_uuid(),$2,'员工',now(),'responses','ready')`, run, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	id, err := CreatePeriod(ctx, st, "monthly", start, end, from)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err = st.Pool.QueryRow(ctx, `SELECT input_manifest FROM wm_log_period_batches WHERE period_run_id=$1`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "日报内容") || !strings.Contains(string(payload), "知识总结") || !strings.Contains(string(payload), "2026-10-29") {
		t.Fatal("missing source or coverage")
	}
	model := &reductionModel{}
	if more, err := AdvancePeriod(ctx, st, id, model, "mock-sol"); err != nil || !more {
		t.Fatalf("model %v %v", more, err)
	}
	if more, err := AdvancePeriod(ctx, st, id, model, "mock-sol"); err != nil || more {
		t.Fatalf("finish %v %v", more, err)
	}
	var state, report string
	if err = st.Pool.QueryRow(ctx, `SELECT state,report FROM wm_log_period_runs WHERE id=$1`, id).Scan(&state, &report); err != nil || state != "partial" || !strings.Contains(report, "知识总结") || !strings.Contains(report, "2026-10-29") || len(model.calls) != 1 || model.calls[0] != "mock-sol" {
		t.Fatalf("state %s report %q calls %v error %v", state, report, model.calls, err)
	}
}

func TestPeriodSplitsLongReportsWithoutTruncation(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 10, 29, 0, 0, 0, 0, loc)
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	want := map[int64]string{}
	for i := 0; i < 3; i++ {
		var id int64
		text := strings.Repeat(fmt.Sprintf("项目%d工作。", i), 8000)
		if err := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,daily_report) VALUES($1,'done',$2) RETURNING id`, from.AddDate(0, 0, i), text).Scan(&id); err != nil {
			t.Fatal(err)
		}
		want[id] = text
	}
	id, err := CreatePeriod(ctx, st, "monthly", start, end, from)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.Pool.Query(ctx, `SELECT input_manifest FROM wm_log_period_batches WHERE period_run_id=$1 ORDER BY ordinal`, id)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := map[int64]*strings.Builder{}
	batchCount := 0
	for rows.Next() {
		batchCount++
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var b workrecords.PlannedBatch
		if err = json.Unmarshal(raw, &b); err != nil {
			t.Fatal(err)
		}
		for _, p := range b.Parts {
			if p.Kind == "daily_report" {
				if rebuilt[p.SourceID] == nil {
					rebuilt[p.SourceID] = &strings.Builder{}
				}
				rebuilt[p.SourceID].WriteString(p.Text)
			}
		}
	}
	rows.Close()
	if batchCount < 2 {
		t.Fatal("long input did not split")
	}
	for source, text := range want {
		if rebuilt[source] == nil || rebuilt[source].String() != text {
			t.Fatalf("source %d changed or omitted", source)
		}
	}
	model := &reductionModel{}
	for i := 0; i < 20; i++ {
		_, err = AdvancePeriod(ctx, st, id, model, "mock-sol")
		if err != nil {
			t.Fatal(err)
		}
		var state string
		if err = st.Pool.QueryRow(ctx, `SELECT state FROM wm_log_period_runs WHERE id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "partial" {
			return
		}
	}
	t.Fatal("multi-level review did not finish")
}

func TestPeriodStopsWhenSourceBecomesPrivate(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	e, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "period-private", Name: "员工"})
	if err != nil {
		t.Fatal(err)
	}
	var run int64
	if err = st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state,daily_report) VALUES('2026-10-31','done','日报') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_sources(run_id,request_id,employee_id,employee_name,started_at,endpoint,status) VALUES($1,gen_random_uuid(),$2,'员工',now(),'responses','ready')`, run, e.ID); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	from := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	id, err := CreatePeriod(ctx, st, "monthly", start, end, from)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	model := &reductionModel{}
	if _, err = AdvancePeriod(ctx, st, id, model, "mock-sol"); err == nil || len(model.calls) != 0 {
		t.Fatalf("privacy failed %v %v", err, model.calls)
	}
}

func TestPeriodSchedulerCreatesOnceAfterMonthEnds(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=true,from_day='2026-10-31' WHERE id=1; INSERT INTO wm_full_journal_runs(day,state,daily_report,synthesis) VALUES('2026-10-31','done','日报','综合')`); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	m := &reductionModel{}
	for i := 0; i < 3; i++ {
		if _, err := SchedulePeriod(ctx, st, from, from, m, "mock-sol"); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	var count int
	var state string
	if err := st.Pool.QueryRow(ctx, `SELECT count(*),min(state) FROM wm_log_period_runs`).Scan(&count, &state); err != nil || count != 1 || state != "partial" {
		t.Fatalf("count=%d state=%s err=%v", count, state, err)
	}
	if more, err := SchedulePeriod(ctx, st, from, from, m, "mock-sol"); err != nil || more {
		t.Fatalf("duplicate scheduling %v %v", more, err)
	}
}

func TestPeriodSchedulerIncludesQuarterlyReview(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_log_worker_control SET enabled=true,from_day='2026-12-31' WHERE id=1; INSERT INTO wm_full_journal_runs(day,state,daily_report,synthesis) VALUES('2026-12-31','done','日报','综合')`); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 12, 31, 0, 0, 0, 0, loc)
	m := &reductionModel{}
	for i := 0; i < 6; i++ {
		if _, err := SchedulePeriod(ctx, st, day, day, m, "mock-sol"); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	var monthly, quarterly int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE kind='monthly' AND state='partial'),count(*) FILTER(WHERE kind='quarterly' AND state='partial') FROM wm_log_period_runs`).Scan(&monthly, &quarterly); err != nil || monthly != 1 || quarterly != 1 {
		t.Fatalf("monthly=%d quarterly=%d err=%v", monthly, quarterly, err)
	}
}
