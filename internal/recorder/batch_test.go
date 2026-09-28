package recorder_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/recorder"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rtCounter counts database round trips: every batch, and every statement
// sent outside a batch.
type rtCounter struct {
	batches, queries atomic.Int64
	inBatch          atomic.Int64
	sessions         atomic.Int64 // BeginSession upserts
}

func (c *rtCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if c.inBatch.Load() == 0 {
		c.queries.Add(1)
		if strings.Contains(d.SQL, "INSERT INTO sessions") {
			c.sessions.Add(1)
		}
	}
	return ctx
}
func (c *rtCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (c *rtCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	c.batches.Add(1)
	c.inBatch.Add(1)
	return ctx
}
func (c *rtCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (c *rtCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {
	c.inBatch.Add(-1)
}

// tracedStore opens a second pool on the test schema with a round-trip counter.
func tracedStore(t *testing.T) (*store.Store, *rtCounter) {
	t.Helper()
	base := testutil.NewDB(t)
	cfg := base.Config()
	c := &rtCounter{}
	cfg.ConnConfig.Tracer = c
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool), c
}

func TestRecorderBatchesRoundTrips(t *testing.T) {
	st, rt := tracedStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	emp := mkEmp(t, st, ctx)
	set := settings.NewService(st)
	_ = set.Get(ctx) // warm the settings cache
	rec := recorder.New(st, set, t.TempDir(), nil, 512)
	rec.Start(ctx)

	baseQ, baseB := rt.queries.Load(), rt.batches.Load()
	const sessions, perSession = 10, 5
	var last [sessions][16]byte
	for turn := 1; turn <= perSession; turn++ {
		for s := 0; s < sessions; s++ {
			inputs := make([]string, turn)
			for i := range inputs {
				inputs[i] = fmt.Sprintf("s%d-turn%d", s, i)
			}
			id := newID()
			last[s] = id
			rec.Submit(recorder.Record{
				RequestID: id, StartedAt: time.Now(), EmployeeID: emp,
				SessionKey: fmt.Sprintf("sess-%d", s), ClientKind: "codex", Endpoint: "responses",
				ReqBody: respBody("responses", inputs), Outcome: outcome("ok"), StatusCode: 200,
			})
		}
	}
	// Let the writer commit on its own (no Flush kicks), then verify.
	time.Sleep(1500 * time.Millisecond)
	rec.Flush()

	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != sessions*perSession {
		t.Fatalf("requests = %d, want %d", n, sessions*perSession)
	}
	if got := rt.sessions.Load(); got != sessions {
		t.Fatalf("session reads = %d, want one per session (%d)", got, sessions)
	}
	queries := rt.queries.Load() - baseQ - 1 // minus the COUNT above
	batches := rt.batches.Load() - baseB
	if queries != sessions || batches > 3 {
		t.Fatalf("%d requests cost %d single queries + %d batches; want %d + <=3",
			sessions*perSession, queries, batches, sessions)
	}

	// Every session's last request must still decode through its prefix chain.
	for s := 0; s < sessions; s++ {
		assertDecodes(t, ctx, st, last[s], perSession)
	}
}

func TestRecorderRejectedRecordIsIsolated(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	spool := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	emp := mkEmp(t, st, ctx)

	// A request id that already exists makes its batch fail (primary key).
	dup := newID()
	if err := st.InsertRequest(ctx, store.RequestRow{ID: dup, StartedAt: time.Now(), EmployeeID: emp,
		ClientKind: "codex", Endpoint: "responses", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}

	rec := recorder.New(st, set, spool, nil, 512)
	rec.Start(ctx)
	a, c := newID(), newID()
	rec.Submit(mkRec(emp, a, "s1", "one"))
	rec.Submit(mkRec(emp, dup, "s2", "boom"))
	rec.Submit(mkRec(emp, c, "s1", "one", "two")) // prefix-encoded onto a
	time.Sleep(1500 * time.Millisecond)           // let all three land in one batch
	rec.Flush()

	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests WHERE id = ANY($1)`, [][]byte{a[:], c[:]}).Scan(&n)
	if n != 2 {
		t.Fatalf("good records written = %d, want 2 (the bad one must not block them)", n)
	}
	assertDecodes(t, ctx, st, c, 2)

	rejected, err := os.ReadFile(filepath.Join(spool, "rejected.jsonl"))
	if err != nil || !strings.Contains(string(rejected), "requests_pkey") {
		t.Fatalf("rejected record not set aside: %v %q", err, rejected)
	}
	if b, err := os.ReadFile(filepath.Join(spool, "pending.jsonl")); err == nil && len(b) > 0 {
		t.Fatalf("a permanently rejected record must not be retried forever: %s", b)
	}
}

func TestRecorderDatabaseDownSpoolsThenReplays(t *testing.T) {
	base := testutil.NewDB(t)
	spool := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	emp := mkEmp(t, store.New(base), ctx)

	// A second pool on the same schema plays the (cross-border) link that drops.
	down, err := pgxpool.NewWithConfig(ctx, base.Config())
	if err != nil {
		t.Fatal(err)
	}
	stDown := store.New(down)
	rec := recorder.New(stDown, settings.NewService(stDown), spool, nil, 512)
	rec.Start(ctx)
	down.Close()

	id := newID()
	rec.Submit(mkRec(emp, id, "s-down", "hello"))
	rec.Flush()
	if b, err := os.ReadFile(filepath.Join(spool, "pending.jsonl")); err != nil || len(b) == 0 {
		t.Fatalf("record not spooled while the database is down: %v", err)
	}

	// The link is back (a fresh process on the healthy pool replays the spool).
	st := store.New(base)
	rec2 := recorder.New(st, settings.NewService(st), spool, nil, 512)
	rec2.Start(ctx)
	rec2.Flush()
	assertDecodes(t, ctx, st, id, 1)
}

func mkRec(emp int64, id [16]byte, sess string, inputs ...string) recorder.Record {
	return recorder.Record{RequestID: id, StartedAt: time.Now(), EmployeeID: emp, SessionKey: sess,
		ClientKind: "codex", Endpoint: "responses", ReqBody: respBody("responses", inputs),
		Outcome: outcome("ok"), StatusCode: 200}
}

// assertDecodes checks a request's full hash list decodes and every blob exists.
func assertDecodes(t *testing.T, ctx context.Context, st *store.Store, id [16]byte, wantInputs int) {
	t.Helper()
	enc, err := st.GetRequestEnc(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	full, err := capture.Decode(toCapture(enc), func(u capture.UUID) (capture.Encoded, error) {
		e, err := st.GetRequestEnc(ctx, [16]byte(u))
		return toCapture(e), err
	})
	if err != nil {
		t.Fatalf("decode %x: %v", id, err)
	}
	// params + instructions + inputs
	if len(full) < wantInputs+2 {
		t.Fatalf("decoded %d hashes, want at least %d", len(full), wantInputs+2)
	}
	for _, h := range full {
		if _, _, err := st.GetBlob(ctx, h); err != nil {
			t.Fatalf("blob %x missing: %v", h, err)
		}
	}
}
