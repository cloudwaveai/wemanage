package workdigest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/workdigest"
)

// recordingSender captures the briefing pushes.
type recordingSender struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordingSender) SendText(_ context.Context, _, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, text)
	return nil
}

// aFriday returns the first Friday at 10:00 after start, in loc.
func aFriday(start time.Time, loc *time.Location) time.Time {
	t := start.In(loc)
	for {
		if t.Weekday() == time.Friday {
			return time.Date(t.Year(), t.Month(), t.Day(), 10, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 0, 1)
	}
}

// seedWeek writes four days of digests (Mon–Thu of the Friday's week) plus
// usage, so a briefing run has material to read.
func seedWeek(t *testing.T, f *fixture, friday time.Time) (monday time.Time) {
	t.Helper()
	loc := friday.Location()
	offset := (int(friday.Weekday()) + 6) % 7
	monday = time.Date(friday.Year(), friday.Month(), friday.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)
	aliceDay := "爱丽丝在改登录页。"
	bobDay := "鲍勃在写网关中间件。"
	svcDay := "平台转发的请求。" // must never appear: service accounts are not people
	for i := 0; i < 4; i++ {
		day := monday.AddDate(0, 0, i)
		if err := f.st.StartWorkDigestDay(f.ctx, day); err != nil {
			t.Fatal(err)
		}
		for _, row := range []store.WorkDigest{
			{Day: day, EmployeeID: f.alice.ID, SummaryMD: aliceDay, Sessions: 2, Messages: 5, Model: "gpt-6-luna"},
			{Day: day, EmployeeID: f.bob.ID, SummaryMD: bobDay, Sessions: 1, Messages: 3, Model: "gpt-6-luna"},
			{Day: day, EmployeeID: f.svc.ID, SummaryMD: svcDay, Sessions: 1, Messages: 1, Model: "gpt-6-luna"},
		} {
			if err := f.st.SaveWorkDigest(f.ctx, row); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.st.FinishWorkDigestDay(f.ctx, day, "团队在做登录页和网关。", "gpt-6-luna", "done", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Usage inside the window, for the table.
	sid := int64(7)
	at := monday.Add(10 * time.Hour)
	add := func(emp store.Employee, in, out int64) {
		t.Helper()
		if err := f.st.InsertRequest(f.ctx, store.RequestRow{
			ID: [16]byte{byte(emp.ID), byte(at.Day())}, StartedAt: at, EmployeeID: emp.ID,
			SessionID: &sid, ClientKind: "codex", Endpoint: "responses", ModelRequested: "gpt-6-luna",
			StatusCode: 200, TokensInput: in, TokensOutput: out,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add(f.alice, 3000, 500)
	add(f.bob, 1000, 200)
	return monday
}

func waitBriefing(t *testing.T, f *fixture, empID int64, week, to time.Time) store.AdminBriefing {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, err := f.st.GetAdminBriefing(f.ctx, empID, week, to)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status != "running" && !f.runner.BriefingRunning(empID) {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("briefing still running after 10s: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunWeekBriefings(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	f.runner.Sender = sender

	friday := aFriday(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.UTC)
	monday := seedWeek(t, f, friday)

	if err := f.runner.RunWeekBriefings(f.ctx, friday); err != nil {
		t.Fatal(err)
	}

	// Alice (super admin) got her briefing, stored and pushed.
	row := waitBriefing(t, f, f.alice.ID, monday, monday.AddDate(0, 0, 4))
	if row.Status != "sent" || row.SentAt == nil {
		t.Fatalf("row = %+v", row)
	}
	sender.mu.Lock()
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0], row.ContentMD) ||
		!strings.Contains(sender.msgs[0], "门户「简报」") {
		t.Fatalf("pushes = %+v", sender.msgs)
	}
	sender.mu.Unlock()

	// The material the model read: both people, both sections, no service
	// account, and the totals come from SQL.
	f.cpa.mu.Lock()
	if len(f.cpa.inputs) != 1 {
		t.Fatalf("%d briefing calls, want 1", len(f.cpa.inputs))
	}
	in := f.cpa.inputs[0]
	model := f.cpa.models[0]
	f.cpa.mu.Unlock()
	for _, want := range []string{"# 团队每日概览", "# 每人所选周期工作", "# 所选周期用量统计", "爱丽丝在改登录页。", "鲍勃在写网关中间件。", "团队合计：请求 2"} {
		if !strings.Contains(in, want) {
			t.Fatalf("material missing %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "平台转发的请求") {
		t.Fatalf("service account leaked into the material:\n%s", in)
	}
	if strings.Contains(in, friday.Format("01-02")) {
		t.Fatalf("material covers Friday, the window ends at its midnight:\n%s", in)
	}
	if want := "acct-" + strconv.FormatInt(f.acc.ID, 10) + "/gpt-6-luna"; model != want {
		t.Fatalf("model = %q, want %q", model, want)
	}

	// Bob is not an admin: no row, no push.
	if _, err := f.st.GetAdminBriefing(f.ctx, f.bob.ID, monday, monday.AddDate(0, 0, 4)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bob has a briefing row: %v", err)
	}

	// A custom prompt replaces the default for the next run.
	if err := f.st.PutAdminBriefingPref(f.ctx, f.alice.ID, true, "只写风险"); err != nil {
		t.Fatal(err)
	}
	friday2 := friday.AddDate(0, 0, 7)
	monday2 := seedWeek(t, f, friday2)
	if err := f.runner.RunWeekBriefings(f.ctx, friday2); err != nil {
		t.Fatal(err)
	}
	row2 := waitBriefing(t, f, f.alice.ID, monday2, monday2.AddDate(0, 0, 4))
	if row2.Status != "sent" {
		t.Fatalf("row2 = %+v", row2)
	}
	f.cpa.mu.Lock()
	systems := append([]string(nil), f.cpa.systems...)
	f.cpa.mu.Unlock()
	if len(systems) != 2 || systems[0] != workdigest.DefaultBriefingPrompt || systems[1] != "只写风险" {
		t.Fatalf("system prompts = %q", systems)
	}
}

func TestRunWeekBriefingsDisabled(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.PutAdminBriefingPref(f.ctx, f.alice.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	f.runner.Sender = sender

	friday := aFriday(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.UTC)
	monday := seedWeek(t, f, friday)
	if err := f.runner.RunWeekBriefings(f.ctx, friday); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetAdminBriefing(f.ctx, f.alice.ID, monday, monday.AddDate(0, 0, 4)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled admin got a row: %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.msgs) != 0 {
		t.Fatalf("disabled admin got pushed: %+v", sender.msgs)
	}
}

func TestRunWeekBriefingsNoMaterial(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	friday := aFriday(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.UTC)
	// No digests at all: silently skips, no rows, no LLM calls.
	if err := f.runner.RunWeekBriefings(f.ctx, friday); err != nil {
		t.Fatal(err)
	}
	f.cpa.mu.Lock()
	n := len(f.cpa.reqs)
	f.cpa.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d LLM calls for an empty week", n)
	}
	if _, err := f.st.GetAdminBriefing(f.ctx, f.alice.ID, friday.AddDate(0, 0, -4), friday); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row created for an empty week: %v", err)
	}
}

// The portal flow: preview with an unsaved prompt, then send.
func TestStartBriefingPreviewAndSend(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	f.runner.Sender = sender

	// Seed the current week; on Monday there is nothing yet.
	from, to := f.runner.WeekBriefingWindow(time.Now())
	if !to.After(from) {
		t.Skip("Monday: the week has no summarized days yet")
	}
	_ = seedWeek(t, f, aFriday(time.Now(), to.Location()))

	if err := f.runner.StartBriefing(f.ctx, f.alice, "试一版提示词", false, from, to); err != nil {
		t.Fatal(err)
	}
	row := waitBriefing(t, f, f.alice.ID, from, to)
	if row.Status != "test" || row.SentAt != nil {
		t.Fatalf("preview row = %+v", row)
	}
	sender.mu.Lock()
	if len(sender.msgs) != 0 {
		t.Fatalf("preview pushed: %+v", sender.msgs)
	}
	sender.mu.Unlock()

	// Send the same week: pushed and marked sent.
	if err := f.runner.StartBriefing(f.ctx, f.alice, "", true, from, to); err != nil {
		t.Fatal(err)
	}
	row = waitBriefing(t, f, f.alice.ID, from, to)
	if row.Status != "sent" || row.SentAt == nil {
		t.Fatalf("send row = %+v", row)
	}
	sender.mu.Lock()
	if len(sender.msgs) != 1 {
		t.Fatalf("pushes = %+v", sender.msgs)
	}
	sender.mu.Unlock()
}

func TestStartBriefingNoMaterial(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	from, to := f.runner.WeekBriefingWindow(time.Now())
	if !to.After(from) {
		t.Skip("Monday: the week has no summarized days yet")
	}
	if err := f.runner.StartBriefing(f.ctx, f.alice, "", false, from, to); err != nil {
		t.Fatal(err)
	}
	row := waitBriefing(t, f, f.alice.ID, from, to)
	if row.Status != "failed" || !strings.Contains(row.Error, "没有可用于简报") {
		t.Fatalf("want an async no-material error, got %+v", row)
	}
}

// blockingCPA lets a test hold the LLM call open.
type blockingCPA struct {
	srv  *httptest.Server
	hold chan struct{}
}

func newBlockingCPA(t *testing.T) *blockingCPA {
	b := &blockingCPA{hold: make(chan struct{})}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-b.hold
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "好了"}}},
		})
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func TestStartBriefingBusy(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	b := newBlockingCPA(t)
	runner := &workdigest.Runner{Store: f.st, Settings: f.set, CPABaseURL: b.srv.URL, CPAAPIKey: "k", Loc: loc}

	from, to := runner.WeekBriefingWindow(time.Now())
	if !to.After(from) {
		t.Skip("Monday: the week has no summarized days yet")
	}
	_ = seedWeek(t, f, aFriday(time.Now(), loc))

	if err := runner.StartBriefing(f.ctx, f.alice, "", false, from, to); err != nil {
		t.Fatal(err)
	}
	if err := runner.StartBriefing(f.ctx, f.alice, "", false, from, to); !errors.Is(err, workdigest.ErrBriefingBusy) {
		t.Fatalf("second start = %v, want busy", err)
	}
	close(b.hold)
	row := waitBriefing(t, f, f.alice.ID, from, to)
	if row.Status != "test" || row.ContentMD != "好了" {
		t.Fatalf("row = %+v", row)
	}
}

func TestBriefingBackfillsSelectedDates(t *testing.T) {
	for _, state := range []string{"missing", "failed", "partial"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			f.configure(t)
			from, to := f.day, f.day.AddDate(0, 0, 1)
			if state != "missing" {
				if err := f.st.StartWorkDigestDay(f.ctx, from); err != nil {
					t.Fatal(err)
				}
				status := "failed"
				if state == "partial" {
					status = "done"
				}
				if err := f.st.FinishWorkDigestDay(f.ctx, from, "旧概览", "m", status, "临时错误"); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.runner.StartBriefing(f.ctx, f.alice, "按项目整理", false, from, to); err != nil {
				t.Fatal(err)
			}
			row := waitBriefing(t, f, f.alice.ID, from, to)
			if row.Status != "test" || row.ContentMD == "" {
				t.Fatalf("briefing = %+v", row)
			}
			day, err := f.st.GetWorkDigestDay(f.ctx, from)
			if err != nil || day.Status != "done" || day.Error != "" {
				t.Fatalf("day = %+v %v", day, err)
			}
			f.cpa.mu.Lock()
			defer f.cpa.mu.Unlock()
			if len(f.cpa.inputs) != 3 {
				t.Fatalf("want employee + overview + briefing, got %d calls", len(f.cpa.inputs))
			}
			material := f.cpa.inputs[2]
			for _, want := range []string{"2026-10-13 至 2026-10-13", "爱丽丝", "改登录页报错文案"} {
				if !strings.Contains(material, want) {
					t.Fatalf("missing %q: %s", want, material)
				}
			}
			if strings.Contains(material, "前一天的事") {
				t.Fatal("out-of-range content included")
			}
		})
	}
}

func TestBriefingRunningVisibleImmediately(t *testing.T) {
	f := setup(t)
	f.configure(t)
	b := newBlockingCPA(t)
	f.runner.CPABaseURL = b.srv.URL
	from, to := f.day, f.day.AddDate(0, 0, 1)
	if err := f.runner.StartBriefing(f.ctx, f.alice, "", false, from, to); err != nil {
		t.Fatal(err)
	}
	row, err := f.st.GetAdminBriefing(f.ctx, f.alice.ID, from, to)
	if err != nil || row.Status != "running" || !f.runner.BriefingRangeRunning(f.alice.ID, from, to) {
		t.Fatalf("first read = %+v %v", row, err)
	}
	if f.runner.BriefingRangeRunning(f.alice.ID, from, to.AddDate(0, 0, 1)) {
		t.Fatal("wrong range reported running")
	}
	if err := f.runner.StartBriefing(f.ctx, f.alice, "", false, from, to.AddDate(0, 0, 1)); !errors.Is(err, workdigest.ErrBriefingBusy) {
		t.Errorf("second start: %v", err)
	}
	close(b.hold)
	_ = waitBriefing(t, f, f.alice.ID, from, to)
}

type failedBriefingSender struct{}

func (failedBriefingSender) SendText(context.Context, string, string) error {
	return errors.New("send unavailable")
}

func TestBriefingKeepsContentOnSendFailure(t *testing.T) {
	f := setup(t)
	f.configure(t)
	f.runner.Sender = failedBriefingSender{}
	if err := f.runner.StartBriefing(f.ctx, f.alice, "", true, f.day, f.day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	row := waitBriefing(t, f, f.alice.ID, f.day, f.day.AddDate(0, 0, 1))
	if row.Status != "failed" || row.ContentMD == "" || row.Model == "" || row.SentAt != nil || !strings.Contains(row.Error, "飞书推送失败") {
		t.Fatalf("lost generated content: %+v", row)
	}
}

func TestWeekBriefingWithoutFeishu(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.st.SetSuperAdmin(f.ctx, f.alice.ID, true); err != nil {
		t.Fatal(err)
	}
	friday := aFriday(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.UTC)
	monday := seedWeek(t, f, friday)
	if err := f.runner.RunWeekBriefings(f.ctx, friday); err != nil {
		t.Fatal(err)
	}
	row := waitBriefing(t, f, f.alice.ID, monday, friday)
	if row.Status != "test" || row.ContentMD == "" || row.SentAt != nil {
		t.Fatalf("without Feishu = %+v", row)
	}
}
