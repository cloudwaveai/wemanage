package report_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/report"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

// cannedSummary intentionally contains a request-count number (777) that
// differs from the true SQL count, to prove numbers are never sourced from
// the model output.
const cannedSummary = "这周借助 AI 完成了登录模块，一共 777 次请求。"

// fakeLLMServer returns an httptest server that always replies with the canned
// summary, plus a pointer to a call counter.
func fakeLLMServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": cannedSummary}},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

type recordedPush struct {
	openID string
	text   string
}

type fakeSender struct {
	mu    sync.Mutex
	texts []recordedPush
	cards []recordedPush
}

func (f *fakeSender) SendText(ctx context.Context, openID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, recordedPush{openID: openID, text: text})
	return nil
}

func (f *fakeSender) SendCard(ctx context.Context, openID string, card any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cards = append(f.cards, recordedPush{openID: openID})
	return nil
}

func (f *fakeSender) textCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

func seedActivity(t *testing.T, st *store.Store, ctx context.Context, empID int64, when time.Time, id [16]byte) {
	t.Helper()
	sess := int64(500)
	if err := st.InsertRequest(ctx, store.RequestRow{
		ID: id, StartedAt: when, EmployeeID: empID, SessionID: &sess,
		Endpoint: "responses", StatusCode: 200,
		TokensInput: 10, TokensOutput: 5, TokensReasoning: 1,
		ClientKind: "codex", ModelRequested: "gpt-5.1-codex",
	}); err != nil {
		t.Fatalf("insert request: %v", err)
	}
	if err := st.InsertTexts(ctx, []store.TextRow{
		{RequestID: id, EmployeeID: empID, SessionID: &sess, StartedAt: when, Role: "user", Text: "帮我做 X"},
		{RequestID: id, EmployeeID: empID, SessionID: &sess, StartedAt: when.Add(time.Second), Role: "assistant", Text: "好的，已完成"},
	}); err != nil {
		t.Fatalf("insert texts: %v", err)
	}
}

func TestGenerateForWeekStoresAndSendsToTheEmployeeOnly(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()

	emp, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_daily", Name: "Zoe"})
	if err != nil {
		t.Fatalf("upsert employee: %v", err)
	}

	loc := time.UTC
	week := time.Date(2026, 9, 21, 0, 0, 0, 0, loc) // a Monday
	if got := report.WeekStart(time.Date(2026, 9, 27, 23, 0, 0, 0, loc), loc); !got.Equal(week) {
		t.Fatalf("WeekStart(Sunday) = %v, want %v", got, week)
	}
	seedActivity(t, st, ctx, emp.ID, week.AddDate(0, 0, 3).Add(10*time.Hour), [16]byte{7})
	// Someone who turned the recap off gets nothing, even with activity.
	off, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_optout", Name: "Opt"})
	_ = st.SetWeeklyReportOff(ctx, off.ID, true)
	seedActivity(t, st, ctx, off.ID, week.AddDate(0, 0, 1), [16]byte{6})

	srv, calls := fakeLLMServer(t)
	sender := &fakeSender{}
	d := report.Deps{
		Store:   st,
		LLM:     report.LLM{BaseURL: srv.URL, Model: "report-model", HC: srv.Client()},
		Sender:  sender,
		Loc:     loc,
		Enabled: true,
	}

	if err := report.GenerateForWeek(ctx, d, week); err != nil {
		t.Fatalf("GenerateForWeek: %v", err)
	}

	if atomic.LoadInt64(calls) == 0 {
		t.Error("expected the LLM to be called at least once")
	}

	rep, err := st.GetReport(ctx, emp.ID, week)
	if err != nil {
		t.Fatalf("GetReport: %v", err)
	}

	// Prose comes from the model.
	if rep.SummaryMD != cannedSummary {
		t.Errorf("summary_md = %q, want %q", rep.SummaryMD, cannedSummary)
	}
	if rep.Model != "report-model" {
		t.Errorf("model = %q, want report-model", rep.Model)
	}

	// Numbers come from SQL (1 request), NOT the model's 777.
	var stats report.Stats
	if err := json.Unmarshal(rep.Stats, &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if stats.Requests != 1 {
		t.Errorf("stats.Requests = %d, want 1 (from SQL, not the model's 777)", stats.Requests)
	}
	if stats.TokensInput != 10 || stats.TokensOutput != 5 {
		t.Errorf("token stats = (in %d, out %d), want (10, 5)", stats.TokensInput, stats.TokensOutput)
	}

	// pushed_at stamped.
	if rep.PushedAt == nil {
		t.Error("pushed_at should be set after a successful push")
	}

	// Exactly one push, to the employee's own open_id.
	if got := sender.textCount(); got != 1 {
		t.Fatalf("push count = %d, want 1", got)
	}
	if sender.texts[0].openID != "ou_daily" {
		t.Errorf("push open_id = %q, want ou_daily", sender.texts[0].openID)
	}
	if !strings.Contains(sender.texts[0].text, "只发给你本人") || !strings.Contains(sender.texts[0].text, "09/21–09/27") {
		t.Errorf("recap text = %q", sender.texts[0].text)
	}
	if _, err := st.GetReport(ctx, off.ID, week); err != store.ErrNotFound {
		t.Errorf("opted-out employee got a report: err = %v", err)
	}
}

func TestGenerateForWeekDisabledNoop(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()

	emp, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_off", Name: "Off"})
	if err != nil {
		t.Fatalf("upsert employee: %v", err)
	}

	loc := time.UTC
	week := time.Date(2026, 9, 21, 0, 0, 0, 0, loc)
	seedActivity(t, st, ctx, emp.ID, week.Add(10*time.Hour), [16]byte{8})

	srv, calls := fakeLLMServer(t)
	sender := &fakeSender{}
	d := report.Deps{
		Store:   st,
		LLM:     report.LLM{BaseURL: srv.URL, Model: "report-model", HC: srv.Client()},
		Sender:  sender,
		Loc:     loc,
		Enabled: false, // disabled
	}

	if err := report.GenerateForWeek(ctx, d, week); err != nil {
		t.Fatalf("GenerateForWeek (disabled): %v", err)
	}

	if n := atomic.LoadInt64(calls); n != 0 {
		t.Errorf("LLM call count = %d, want 0 when disabled", n)
	}
	if n := sender.textCount(); n != 0 {
		t.Errorf("push count = %d, want 0 when disabled", n)
	}
	if _, err := st.GetReport(ctx, emp.ID, week); err != store.ErrNotFound {
		t.Errorf("GetReport err = %v, want ErrNotFound (no rows written)", err)
	}
}
