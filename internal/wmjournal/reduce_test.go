package wmjournal

import (
	"context"
	"strings"
	"testing"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestReductionRequiresEveryBatchAndUsesOnlyLogResults(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	var run int64
	if err := st.Pool.QueryRow(ctx, `INSERT INTO wm_full_journal_runs(day,state) VALUES('2026-10-29','running') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO wm_full_journal_batches(run_id,phase,ordinal,input_manifest,state,raw_output) VALUES($1,'extract',0,'{}','done','证据一'),($1,'extract',1,'{}','queued','')`, run); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareReduction(ctx, st, run, "extract", 0); err == nil {
		t.Fatal("accepted incomplete layer")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE wm_full_journal_batches SET state='done',raw_output='证据二' WHERE run_id=$1 AND ordinal=1`, run); err != nil {
		t.Fatal(err)
	}
	phase, _, err := PrepareReduction(ctx, st, run, "extract", 0)
	if err != nil || phase != "daily" {
		t.Fatalf("%s %v", phase, err)
	}
	var raw []byte
	if err = st.Pool.QueryRow(ctx, `SELECT input_manifest FROM wm_full_journal_batches WHERE run_id=$1 AND phase='daily'`, run).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "证据一") || !strings.Contains(string(raw), "证据二") || strings.Contains(string(raw), "gateway_daily_context") {
		t.Fatal("incorrect reduction input")
	}
}
