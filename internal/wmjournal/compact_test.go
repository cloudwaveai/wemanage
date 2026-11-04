package wmjournal

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/wemanage"
)

func TestCompactGroupsRepeatedLogsAndBuildsDaily(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	e, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "compact-test", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	var run, source int64
	if err = st.Pool.QueryRow(ctx, `insert into wm_full_journal_runs(day,state) values('2026-10-29','running') returning id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"role":"user","type":"message","content":"需要完成日报"}`)
	hash := sha256.Sum256(data)
	if _, err = st.Pool.Exec(ctx, `insert into wm_full_journal_blobs(run_id,hash,kind,data) values($1,$2,'input',$3)`, run, hash[:], data); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = st.Pool.QueryRow(ctx, `insert into wm_full_journal_sources(run_id,request_id,employee_id,employee_name,session_id,started_at,endpoint,status) values($1,gen_random_uuid(),$2,'成员',12,now(),'responses','ready') returning id`, run, e.ID).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if _, err = st.Pool.Exec(ctx, `insert into wm_full_journal_segments(source_id,direction,ordinal,hash,kind) values($1,'request',0,$2,'input')`, source, hash[:]); err != nil {
			t.Fatal(err)
		}
	}
	n, err := PlanCompact(ctx, st, run)
	if err != nil || n != 1 {
		var detail string
		_ = st.Pool.QueryRow(ctx, `select string_agg(status||':'||coalesce(session_id::text,'null')||':'||(select count(*)::text from wm_full_journal_segments g where g.source_id=s.id)||':'||(select count(*)::text from wm_full_journal_segments g left join wm_full_journal_blobs b on b.run_id=s.run_id and b.hash=g.hash where g.source_id=s.id and b.hash is null),',') from wm_full_journal_sources s where run_id=$1`, run).Scan(&detail)
		t.Fatalf("plan %d: %v detail=%s", n, err, detail)
	}
	var input string
	if err = st.Pool.QueryRow(ctx, `select input from wm_compact_jobs where run_id=$1`, run).Scan(&input); err != nil || strings.Count(input, "需要完成日报") != 1 || !strings.Contains(input, "出现 2 次") {
		t.Fatalf("input %q err=%v", input, err)
	}
	m := &reductionModel{}
	for i := 0; i < 3; i++ {
		if _, err = CompactStep(ctx, st, run, m, "mock"); err != nil {
			t.Fatal(err)
		}
		more, err := PrepareCompactNext(ctx, st, run)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && !more {
			t.Fatalf("stopped at %d", i)
		}
		if i == 2 && more {
			t.Fatal("did not finish")
		}
	}
	var output string
	if err = st.Pool.QueryRow(ctx, `select output from wm_compact_jobs where run_id=$1 and phase='daily' and state='done'`, run).Scan(&output); err != nil || output == "" {
		t.Fatalf("daily=%q err=%v", output, err)
	}
	if _, err = st.Pool.Exec(ctx, `update employees set private_work_since=now() where id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = CompactStep(ctx, st, run, m, "mock"); err == nil {
		t.Fatal("private source accepted")
	}
}

func TestPackPreservesEveryQueuedInput(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	if err := wemanage.Install(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, CompactSchema); err != nil {
		t.Fatal(err)
	}
	for i, s := range []string{"一", "二", "三", "四"} {
		if _, err := st.Pool.Exec(ctx, `insert into wm_compact_jobs(run_id,employee_id,session_id,phase,ordinal,input) values(91,1,2,'extract',$1,$2)`, i, s); err != nil {
			t.Fatal(err)
		}
	}
	n, err := PackCompact(ctx, st, 91)
	if err != nil || n != 2 {
		t.Fatalf("packed=%d err=%v", n, err)
	}
	var before, after string
	var old, new int
	err = st.Pool.QueryRow(ctx, `select (select string_agg(input,'' order by ordinal) from wm_compact_jobs where run_id=91 and phase='source'),(select string_agg(input,'' order by ordinal) from wm_compact_jobs where run_id=91 and phase='extract'),(select count(*) from wm_compact_jobs where run_id=91 and phase='source'),(select count(*) from wm_compact_jobs where run_id=91 and phase='extract')`).Scan(&before, &after, &old, &new)
	if err != nil || old != 4 || new != 2 || strings.ReplaceAll(after, "\n", "") != before {
		t.Fatalf("before=%q after=%q old=%d new=%d err=%v", before, after, old, new, err)
	}
}
