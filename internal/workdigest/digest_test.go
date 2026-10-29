package workdigest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/workdigest"
)

// fakeCPA answers chat completions like CPA does for a ChatGPT account and
// records what it was asked.
type fakeCPA struct {
	mu       sync.Mutex
	reqs     []*http.Request
	models   []string
	inputs   []string
	systems  []string // the system prompt of each call
	failWhen string   // reply 500 when the system prompt contains this
}

func (f *fakeCPA) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		f.models = append(f.models, in.Model)
		f.inputs = append(f.inputs, in.Messages[1].Content)
		f.systems = append(f.systems, in.Messages[0].Content)
		f.mu.Unlock()
		if f.failWhen != "" && strings.Contains(in.Messages[0].Content, f.failWhen) {
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
			return
		}
		reply := "团队今天在做登录页。"
		if m := regexp.MustCompile(`员工：(\S+)`).FindStringSubmatch(in.Messages[1].Content); m != nil {
			reply = m[1] + "：改登录页报错文案。"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fixture struct {
	st     *store.Store
	set    *settings.Service
	ctx    context.Context
	day    time.Time
	alice  store.Employee
	bob    store.Employee
	svc    store.Employee
	acc    store.Account
	runner *workdigest.Runner
	cpa    *fakeCPA
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	loc := time.FixedZone("CST", 8*3600)
	f := &fixture{st: st, set: set, ctx: ctx, cpa: &fakeCPA{}}
	f.day = time.Date(2026, 10, 13, 0, 0, 0, 0, loc)

	var err error
	if f.alice, err = st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_a", Name: "爱丽丝"}); err != nil {
		t.Fatal(err)
	}
	if f.bob, err = st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_b", Name: "鲍勃"}); err != nil {
		t.Fatal(err)
	}
	if f.svc, err = st.CreateServiceAccount(ctx, "New API 平台"); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateIPGroup(ctx, box, "g1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if f.acc, err = st.CreateAccount(ctx, store.Account{CPAAuthName: "plus.json", Email: "plus@x", IPGroupID: g.ID,
		DeviceProfile: store.DeviceProfile{OS: "Mac OS 26.5.2; arm64", Terminal: "iTerm.app/3.6.11"}}); err != nil {
		t.Fatal(err)
	}

	sid := int64(7)
	at := f.day.Add(10 * time.Hour)
	texts := []store.TextRow{
		{EmployeeID: f.alice.ID, SessionID: &sid, StartedAt: at, Role: "user", Text: "<environment_context>x</environment_context>"},
		{EmployeeID: f.alice.ID, SessionID: &sid, StartedAt: at, Role: "user", Text: "把登录页报错改成中文"},
		{EmployeeID: f.alice.ID, SessionID: &sid, StartedAt: at.Add(time.Minute), Role: "assistant", Text: "改好了"},
		{EmployeeID: f.alice.ID, SessionID: &sid, StartedAt: at.Add(-24 * time.Hour), Role: "user", Text: "前一天的事"},
		{EmployeeID: f.bob.ID, SessionID: &sid, StartedAt: at, Role: "user", Text: "# AGENTS.md instructions"},
		{EmployeeID: f.svc.ID, SessionID: &sid, StartedAt: at, Role: "user", Text: "平台转发的请求"},
	}
	if err := st.InsertTexts(ctx, texts); err != nil {
		t.Fatal(err)
	}

	srv := f.cpa.server(t)
	f.runner = &workdigest.Runner{Store: st, Settings: set, CPABaseURL: srv.URL, CPAAPIKey: "cpa-key", Loc: loc}
	return f
}

func (f *fixture) configure(t *testing.T) {
	t.Helper()
	s := f.set.Get(f.ctx)
	s.WorkDigestAccountID = f.acc.ID
	if err := f.set.Put(f.ctx, s); err != nil {
		t.Fatal(err)
	}
}

func TestRunDay(t *testing.T) {
	f := setup(t)
	f.configure(t)
	if err := f.runner.RunDay(f.ctx, f.day.Add(15*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Only Alice typed anything that day; Bob's turn is client context and
	// the service account is not a person.
	digests, err := f.st.WorkDigests(f.ctx, f.day)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 || digests[0].EmployeeID != f.alice.ID || digests[0].SummaryMD != "爱丽丝：改登录页报错文案。" {
		t.Fatalf("digests = %+v", digests)
	}
	if digests[0].Sessions != 1 || digests[0].Messages != 1 || digests[0].Model != "gpt-6-luna" {
		t.Fatalf("digest counts/model = %+v", digests[0])
	}
	d, err := f.st.GetWorkDigestDay(f.ctx, f.day)
	if err != nil || d.Status != "done" || d.OverviewMD != "团队今天在做登录页。" || d.Error != "" {
		t.Fatalf("day = %+v %v", d, err)
	}

	f.cpa.mu.Lock()
	defer f.cpa.mu.Unlock()
	if len(f.cpa.reqs) != 2 {
		t.Fatalf("%d calls, want employee + overview", len(f.cpa.reqs))
	}
	if strings.Contains(f.cpa.inputs[0], "前一天的事") || strings.Contains(f.cpa.inputs[0], "environment_context") {
		t.Fatalf("material leaked other days or context:\n%s", f.cpa.inputs[0])
	}
	if !strings.Contains(f.cpa.inputs[1], "### 爱丽丝") {
		t.Fatalf("overview input = %s", f.cpa.inputs[1])
	}
	r := f.cpa.reqs[0]
	want := "acct-" + strconv.FormatInt(f.acc.ID, 10) + "/gpt-6-luna"
	if f.cpa.models[0] != want || r.Header.Get("Authorization") != "Bearer cpa-key" {
		t.Fatalf("model %q auth %q, want %q", f.cpa.models[0], r.Header.Get("Authorization"), want)
	}
	if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "codex-tui/") || !strings.Contains(ua, "Mac OS 26.5.2; arm64") {
		t.Fatalf("User-Agent = %q, want the account's Codex identity", ua)
	}
	if r.Header.Get("Originator") == "" || r.Header.Get("Session-Id") == "" {
		t.Fatalf("identity headers missing: %v", r.Header)
	}
}

func TestRunDayNotConfigured(t *testing.T) {
	f := setup(t)
	if err := f.runner.RunDay(f.ctx, f.day); err != nil {
		t.Fatal(err)
	}
	if len(f.cpa.reqs) != 0 {
		t.Fatal("called the LLM without an account set up")
	}
	if err := f.runner.Start(f.day); err == nil {
		t.Fatal("Start without an account should say so")
	}
}

// A failed overview marks the day failed but keeps the employee summaries.
func TestRunDayOverviewFails(t *testing.T) {
	f := setup(t)
	f.configure(t)
	f.cpa.failWhen = "团队工作概览"
	if err := f.runner.RunDay(f.ctx, f.day); err == nil {
		t.Fatal("want an error")
	}
	d, err := f.st.GetWorkDigestDay(f.ctx, f.day)
	if err != nil || d.Status != "failed" || !strings.Contains(d.Error, "团队概览没能生成") {
		t.Fatalf("day = %+v %v", d, err)
	}
	if digests, _ := f.st.WorkDigests(f.ctx, f.day); len(digests) != 1 {
		t.Fatalf("employee summaries = %+v", digests)
	}
}
