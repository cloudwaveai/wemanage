package wmjournal

import (
	"context"
	"strings"
	"testing"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
)

type reductionModel struct{ calls []string }

func (m *reductionModel) Extract(_ context.Context, _, _ string) (string, string, workdigest.ModelUsage, error) {
	m.calls = append(m.calls, "daily")
	return "日报内容", "mock", workdigest.ModelUsage{}, nil
}
func (m *reductionModel) ExtractWithModel(_ context.Context, model, _, _ string) (string, string, workdigest.ModelUsage, error) {
	m.calls = append(m.calls, model)
	return "知识总结", model, workdigest.ModelUsage{}, nil
}

func TestAdvanceProducesDailyAndSolWithVisibleGap(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	var run int64
	if err := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','running') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state,raw_output) VALUES($1,'extract',0,'{}','done','[覆盖缺口：媒体未分析]')`, run); err != nil {
		t.Fatal(err)
	}
	m := &reductionModel{}
	for i := 0; i < 5; i++ {
		_, err := AdvanceReduction(ctx, st, run, m, "mock-sol")
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	var status, daily, synthesis string
	if err := st.Pool.QueryRow(ctx, `SELECT state,daily_report,synthesis FROM wm_full_journal_runs WHERE id=$1`, run).Scan(&status, &daily, &synthesis); err != nil {
		t.Fatal(err)
	}
	if status != "partial" || !strings.Contains(daily, "媒体内容尚未分析") || !strings.Contains(daily, "日报内容") || synthesis != "知识总结" || len(m.calls) != 2 || m.calls[1] != "mock-sol" {
		t.Fatalf("status=%s daily=%q synthesis=%q calls=%v", status, daily, synthesis, m.calls)
	}
}
