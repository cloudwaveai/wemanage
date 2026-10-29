package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"
	"aiseat/internal/workdigest"

	"github.com/go-chi/chi/v5"
)

// briefingCPA answers chat completions and records the system prompts.
type briefingCPA struct {
	mu      sync.Mutex
	systems []string
}

func (b *briefingCPA) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		b.mu.Lock()
		b.systems = append(b.systems, in.Messages[0].Content)
		b.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "本周简报内容"}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

type pushRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (p *pushRecorder) SendText(_ context.Context, _, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, text)
	return nil
}

// newBriefingServer wires the web server with a real digest Runner backed by
// a fake CPA, so the briefing endpoints run end to end.
func newBriefingServer(t *testing.T) (*httptest.Server, *store.Store, context.Context, *workdigest.Runner, *pushRecorder) {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	cpa := &briefingCPA{}
	srvCPA := cpa.server(t)
	g, err := st.CreateIPGroup(context.Background(), box, "g1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.CreateAccount(context.Background(), store.Account{CPAAuthName: "plus.json", Email: "plus@x",
		IPGroupID: g.ID, DeviceProfile: store.DeviceProfile{OS: "Mac OS 26.5.2; arm64", Terminal: "iTerm.app/3.6.11"}})
	if err != nil {
		t.Fatal(err)
	}
	s := set.Get(context.Background())
	s.WorkDigestAccountID = acc.ID
	if err := set.Put(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	runner := &workdigest.Runner{Store: st, Settings: set, CPABaseURL: srvCPA.URL, CPAAPIKey: "cpa-key"}
	pushes := &pushRecorder{}
	runner.Sender = pushes
	srv := web.NewServer(web.Deps{
		Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true,
		Digest: runner,
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, st, context.Background(), runner, pushes
}

// seedCurrentWeek writes digests for every day the briefing window covers.
func seedCurrentWeek(t *testing.T, st *store.Store, ctx context.Context, r *workdigest.Runner, empID int64) {
	t.Helper()
	from, to := r.WeekBriefingWindow(time.Now())
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		if err := st.StartWorkDigestDay(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: d, EmployeeID: empID, SummaryMD: "改登录页。", Sessions: 1, Messages: 2}); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishWorkDigestDay(ctx, d, "团队在做登录页。", "gpt-6-luna", "done", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdminBriefing(t *testing.T) {
	ts, st, ctx, runner, pushes := newBriefingServer(t)
	_ = st.EnsureSuperAdmin(ctx, "ou_boss", "老板")
	boss := login(t, ts, "ou_boss")
	alice := login(t, ts, "ou_alice")
	bossEmp, err := st.GetEmployeeByOpenID(ctx, "ou_boss")
	if err != nil {
		t.Fatal(err)
	}

	// Employees are shut out; a super admin reads their defaults.
	if resp, _ := alice.do(http.MethodGet, "/api/admin/briefing", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("employee got %d, want 403", resp.StatusCode)
	}
	resp, body := boss.do(http.MethodGet, "/api/admin/briefing", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var cfg struct {
		Enabled  bool
		Prompt   string
		Running  bool
		Week     *map[string]any `json:"briefing"`
		History  []map[string]any
		WeekFrom string `json:"from"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Prompt != "" || cfg.Running || cfg.Week != nil || len(cfg.History) != 0 {
		t.Fatalf("defaults = %+v", cfg)
	}

	// Save a custom prompt and a switch-off.
	resp, _ = boss.do(http.MethodPut, "/api/admin/briefing", `{"enabled":true,"prompt":"只写风险"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("put status %d", resp.StatusCode)
	}
	resp, body = boss.do(http.MethodGet, "/api/admin/briefing", "")
	if err := json.Unmarshal(body, &cfg); err != nil || cfg.Prompt != "只写风险" {
		t.Fatalf("saved cfg = %s (%v)", body, err)
	}

	// Seed the window and preview with an unsaved prompt.
	from, to := runner.WeekBriefingWindow(time.Now())
	if !to.After(from) {
		t.Skip("Monday: the week has no summarized days yet")
	}

	seedCurrentWeek(t, st, ctx, runner, bossEmp.ID)
	resp, _ = boss.do(http.MethodPost, "/api/admin/briefing/preview", `{"prompt":"试一版"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("preview status %d", resp.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	var week map[string]any
	for {
		_, body = boss.do(http.MethodGet, "/api/admin/briefing", "")
		if err := json.Unmarshal(body, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Week != nil {
			week = *cfg.Week
			if week["status"] != "running" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("preview never finished: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if week["status"] != "test" || week["content_md"] != "本周简报内容" {
		t.Fatalf("week = %+v", week)
	}
	pushes.mu.Lock()
	if len(pushes.msgs) != 0 {
		t.Fatalf("preview pushed: %+v", pushes.msgs)
	}
	pushes.mu.Unlock()

	// Send it: the prompt is saved and the briefing lands in Feishu.
	resp, _ = boss.do(http.MethodPost, "/api/admin/briefing/send", `{"prompt":"只写风险"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d", resp.StatusCode)
	}
	for {
		_, body = boss.do(http.MethodGet, "/api/admin/briefing", "")
		if err := json.Unmarshal(body, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Week != nil {
			week = *cfg.Week
			if week["status"] != "running" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("send never finished: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if week["status"] != "sent" || week["sent_at"] == nil {
		t.Fatalf("sent week = %+v", week)
	}
	pushes.mu.Lock()
	if len(pushes.msgs) != 1 || !strings.Contains(pushes.msgs[0], "本周简报内容") {
		t.Fatalf("pushes = %+v", pushes.msgs)
	}
	pushes.mu.Unlock()

	// The custom prompt was persisted by the send, and history lists the week.
	_, body = boss.do(http.MethodGet, "/api/admin/briefing", "")
	if err := json.Unmarshal(body, &cfg); err != nil || cfg.Prompt != "只写风险" || len(cfg.History) != 1 {
		t.Fatalf("after send = %s (%v)", body, err)
	}

	// Browsing another (unwritten) week returns no row.
	other := from.AddDate(0, 0, -7).Format("2006-01-02")
	resp, body = boss.do(http.MethodGet, "/api/admin/briefing?from="+other+"&to="+from.AddDate(0, 0, -4).Format(time.DateOnly), "")
	if resp.StatusCode != 200 {
		t.Fatalf("history week status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &cfg); err != nil || cfg.Week != nil {
		t.Fatalf("other week = %s (%v)", body, err)
	}
}

func TestAdminBriefingSelectedDates(t *testing.T) {
	ts, st, ctx, _, _ := newBriefingServer(t)
	if err := st.EnsureSuperAdmin(ctx, "ou_dates", "管理员"); err != nil {
		t.Fatal(err)
	}
	boss := login(t, ts, "ou_dates")
	emp, err := st.GetEmployeeByOpenID(ctx, "ou_dates")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)
	if err := st.StartWorkDigestDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: emp.ID, SummaryMD: "完成登录功能"}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkDigestDay(ctx, day, "团队完成登录功能", "m", "done", ""); err != nil {
		t.Fatal(err)
	}
	for _, end := range []string{"2026-10-13", "2026-10-14"} {
		bodyText := `{"from":"2026-10-13","to":"` + end + `","prompt":"只写进展"}`
		resp, body := boss.do(http.MethodPost, "/api/admin/briefing/preview", bodyText)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("start %s: %d %s", end, resp.StatusCode, body)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			resp, body = boss.do(http.MethodGet, "/api/admin/briefing?from=2026-10-13&to="+end, "")
			var result struct {
				From, To string
				Running  bool
				Briefing struct {
					Status  string `json:"status"`
					Content string `json:"content_md"`
				}
			}
			if err := json.Unmarshal(body, &result); err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("get: %d %s %v", resp.StatusCode, body, err)
			}
			if result.From != "2026-10-13" || result.To != end {
				t.Fatalf("range changed: %s", body)
			}
			if !result.Running {
				if result.Briefing.Status != "test" || result.Briefing.Content != "本周简报内容" {
					t.Fatalf("result: %s", body)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("never finished: %s", body)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	_, body := boss.do(http.MethodGet, "/api/admin/briefing?from=2026-10-13&to=2026-10-13", "")
	var history struct{ History []map[string]any }
	if err := json.Unmarshal(body, &history); err != nil || len(history.History) != 2 {
		t.Fatalf("independent ranges not preserved: %s %v", body, err)
	}
	for _, dates := range []string{
		`"from":"2026-10-14","to":"2026-10-13"`,
		`"from":"2026-09-01","to":"2026-10-13"`,
		`"from":"2026-10-13"`,
		`"from":"2026-02-30","to":"2026-10-13"`,
		`"from":"2099-01-01","to":"2099-01-01"`,
	} {
		resp, body := boss.do(http.MethodPost, "/api/admin/briefing/preview", "{"+dates+"}")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid range accepted: %s: %d %s", dates, resp.StatusCode, body)
		}
	}
	resp, _ := boss.do(http.MethodPost, "/api/admin/briefing/preview", `{"prompt":"`+strings.Repeat("字", 10001)+`"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized prompt accepted: %d", resp.StatusCode)
	}
}
