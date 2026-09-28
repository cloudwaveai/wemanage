package proxy_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/proxy"
	"aiseat/internal/recorder"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"

	"github.com/go-chi/chi/v5"
)

type fakeCPA struct {
	srv           *httptest.Server
	mu            sync.Mutex
	lastModel     string
	lastUA        string
	lastAuth      string
	lastForwarded string
	inFlight      int32
	maxInFlight   int32
	status        int
	respHeader    map[string]string
	sse           string
}

func newFakeCPA() *fakeCPA {
	f := &fakeCPA{status: 200}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&f.inFlight, 1)
		for {
			old := atomic.LoadInt32(&f.maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&f.maxInFlight, old, cur) {
				break
			}
		}
		defer atomic.AddInt32(&f.inFlight, -1)
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.lastModel = gjsonModel(body)
		f.lastUA = r.Header.Get("User-Agent")
		f.lastAuth = r.Header.Get("Authorization")
		f.lastForwarded = r.Header.Get("X-Forwarded-For")
		st, sh, sse := f.status, f.respHeader, f.sse
		f.mu.Unlock()
		for k, v := range sh {
			w.Header().Set(k, v)
		}
		if st != 200 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if sse == "" {
			sse = defaultSSE
		}
		time.Sleep(15 * time.Millisecond) // widen the concurrency window
		_, _ = io.WriteString(w, sse)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"acct-1/gpt-5.1-codex"},{"id":"acct-2/other"}]}`)
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func gjsonModel(b []byte) string {
	// tiny extraction to avoid importing gjson in tests
	s := string(b)
	i := strings.Index(s, `"model":"`)
	if i < 0 {
		return ""
	}
	s = s[i+len(`"model":"`):]
	j := strings.IndexByte(s, '"')
	return s[:j]
}

const defaultSSE = `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hi"}

event: response.completed
data: {"type":"response.completed","response":{"model":"gpt-5.1-codex","usage":{"input_tokens":10,"output_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}}

`

type rig struct {
	r     *chi.Mux
	st    *store.Store
	set   *settings.Service
	key   string
	rec   *recorder.Recorder
	eng   *risk.Engine
	empID int64
	ctx   context.Context
}

func harness(t *testing.T, cpa *fakeCPA) (*chi.Mux, *store.Store, string, *recorder.Recorder, context.Context) {
	h := newRig(t, cpa, settings.RiskStandard)
	return h.r, h.st, h.key, h.rec, h.ctx
}

func newRig(t *testing.T, cpa *fakeCPA, mode string) *rig {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := settings.Defaults()
	s.RiskMode = mode
	if err := set.Put(ctx, s); err != nil {
		t.Fatal(err)
	}

	e, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_p", Name: "p"})
	_ = st.AckNotice(ctx, e.ID, settings.Defaults().NoticeVersion)
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	a, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "c.json", IPGroupID: g.ID, DeviceProfile: store.DeviceProfile{OS: "Linux", Terminal: "x"}})
	_ = st.BindAccount(ctx, a.ID, e.ID)
	plain, _, _ := st.IssueKey(ctx, e.ID)

	rec := recorder.New(st, set, t.TempDir(), nil, 128)
	rec.Start(ctx)
	eng := risk.NewEngine(risk.Config{Store: st, Settings: set, PortalURL: "https://portal"})
	eng.Start(ctx)

	h := proxy.NewHandler(proxy.HandlerDeps{
		Store: st, Settings: set, Limiter: proxy.NewLimiter(), Recorder: rec,
		CPABaseURL: cpa.srv.URL, CPAAPIKey: "sk-internal", Upstream: cpa.srv.Client(),
		Risk: eng, TrustProxy: true,
	})
	r := chi.NewRouter()
	h.Routes(r)
	t.Cleanup(func() { cpa.srv.Close() })
	return &rig{r: r, st: st, set: set, key: plain, rec: rec, eng: eng, empID: e.ID, ctx: ctx}
}

func doReq(t *testing.T, r http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex-tui/0.157.1 (Linux; x64) x (codex-tui; 0.157.1)")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestProxyHappyPathPrefixAndHeaders(t *testing.T) {
	cpa := newFakeCPA()
	r, _, key, rec, _ := harness(t, cpa)

	w := doReq(t, r, key, `{"model":"gpt-5.1-codex","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatalf("stream not relayed: %s", w.Body.String())
	}
	rec.Flush()
	cpa.mu.Lock()
	defer cpa.mu.Unlock()
	if !strings.HasPrefix(cpa.lastModel, "acct-") || !strings.HasSuffix(cpa.lastModel, "/gpt-5.1-codex") {
		t.Fatalf("upstream model = %q, want acct-<id>/gpt-5.1-codex", cpa.lastModel)
	}
	if cpa.lastAuth != "Bearer sk-internal" {
		t.Fatalf("upstream auth = %q", cpa.lastAuth)
	}
	if cpa.lastForwarded != "" {
		t.Fatalf("X-Forwarded-For leaked upstream: %q", cpa.lastForwarded)
	}
	if !strings.Contains(cpa.lastUA, "codex-tui/0.157.1") {
		t.Fatalf("codex UA not passed through: %q", cpa.lastUA)
	}
}

func TestProxyConcurrencyCappedAtFour(t *testing.T) {
	cpa := newFakeCPA()
	r, _, key, _, _ := harness(t, cpa)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&cpa.maxInFlight); got > 4 {
		t.Fatalf("max upstream concurrency = %d, want <= 4", got)
	}
	if got := atomic.LoadInt32(&cpa.maxInFlight); got < 2 {
		t.Fatalf("expected real concurrency, max = %d", got)
	}
}

func TestProxyInvalidKey(t *testing.T) {
	cpa := newFakeCPA()
	r, _, _, _, _ := harness(t, cpa)
	w := doReq(t, r, "sk-emp-bogus", `{"model":"gpt-5.1-codex"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_request_error") {
		t.Fatalf("not OpenAI error shape: %s", w.Body.String())
	}
}

func TestProxyNoticeRequired(t *testing.T) {
	cpa := newFakeCPA()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	ctx := context.Background()
	e, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_n", Name: "n"})
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	a, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "c.json", IPGroupID: g.ID, DeviceProfile: store.DeviceProfile{}})
	_ = st.BindAccount(ctx, a.ID, e.ID)
	plain, _, _ := st.IssueKey(ctx, e.ID) // note: NoticeAckAt is nil
	rec := recorder.New(st, set, t.TempDir(), nil, 16)
	rec.Start(ctx)
	h := proxy.NewHandler(proxy.HandlerDeps{Store: st, Settings: set, Limiter: proxy.NewLimiter(), Recorder: rec, CPABaseURL: cpa.srv.URL, CPAAPIKey: "x", Upstream: cpa.srv.Client()})
	r := chi.NewRouter()
	h.Routes(r)
	defer cpa.srv.Close()

	w := doReq(t, r, plain, `{"model":"gpt-5.1-codex"}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "notice_required") {
		t.Fatalf("status=%d body=%s, want 403 notice_required", w.Code, w.Body.String())
	}
}

func TestProxyUpstreamRateLimitSetsAccount(t *testing.T) {
	cpa := newFakeCPA()
	reset := time.Now().Add(2 * time.Hour).Unix()
	cpa.status = 429
	cpa.respHeader = map[string]string{"X-Codex-Primary-Reset-At": fmt.Sprintf("%d", reset)}
	r, st, key, rec, ctx := harness(t, cpa)

	w := doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	if w.Code != 429 {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	rec.Flush()
	// account should now be marked rate limited
	accs, _ := st.ListAccounts(ctx)
	if len(accs) != 1 || accs[0].RateLimitedUntil == nil {
		t.Fatalf("account not rate-limited: %+v", accs)
	}
	// a subsequent request is short-circuited to 429 without hitting upstream
	before := cpa.lastModel
	w2 := doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	if w2.Code != 429 {
		t.Fatalf("second status = %d, want 429", w2.Code)
	}
	_ = before
}

func TestProxyModelMismatchSignal(t *testing.T) {
	cpa := newFakeCPA()
	cpa.sse = `event: response.completed
data: {"type":"response.completed","response":{"model":"gpt-5.6-luna","usage":{"input_tokens":1,"output_tokens":1},"output":[]}}

`
	r, st, key, rec, ctx := harness(t, cpa)
	doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	rec.Flush()
	time.Sleep(50 * time.Millisecond) // reactToUpstream runs after Submit
	accs, _ := st.ListAccounts(ctx)
	has, _ := st.HasOpenSignal(ctx, accs[0].ID, "model_mismatch")
	if !has {
		t.Fatal("expected model_mismatch signal")
	}
}

const codexBody = `{"model":"gpt-5.1-codex","input":[],"client_metadata":{"x-codex-installation-id":"inst-1"}}`

func doUA(t *testing.T, r http.Handler, key, ua, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func wantCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || (code != "" && !strings.Contains(w.Body.String(), `"code":"`+code+`"`)) {
		t.Fatalf("status=%d body=%s, want %d %s", w.Code, w.Body.String(), status, code)
	}
}

func TestProxyBlocksNonCodexClient(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	w := doUA(t, h.r, h.key, "curl/8.7.1", codexBody)
	wantCode(t, w, http.StatusForbidden, "client_not_allowed")
	h.eng.Flush()
	evs, _ := h.st.ListRiskEvents(h.ctx, store.RiskEventFilter{EmployeeID: &h.empID})
	if len(evs) != 1 || evs[0].Rule != "non_codex_blocked" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestProxyFrozenEmployee(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	if _, err := h.st.FreezeEmployee(h.ctx, h.empID, "test"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusForbidden, "account_frozen")
}

func TestProxyStandardRegistersPendingDevice(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
	h.rec.Flush()
	h.eng.Flush()

	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	if len(devs) != 1 || devs[0].DeviceKey != "inst:inst-1" || devs[0].Status != store.DevicePending {
		t.Fatalf("devices: %+v", devs)
	}
	var ip string
	var dev *int64
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT client_ip, device_id FROM requests`).Scan(&ip, &dev); err != nil {
		t.Fatal(err)
	}
	// httptest's RemoteAddr is a public TEST-NET address, so X-Forwarded-For is ignored.
	if ip != "192.0.2.1" || dev == nil || *dev != devs[0].ID {
		t.Fatalf("request client_ip=%q device=%v", ip, dev)
	}
}

func TestProxyPendingPastConfirmWindow(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
	h.eng.Flush()
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE client_devices SET first_seen = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	h.eng.DeviceChanged(h.empID, devs[0].ID, store.DevicePending) // drop the gate cache
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusForbidden, "device_unconfirmed")

	// Once the employee confirms, the device works again.
	_ = h.st.SetDeviceStatus(h.ctx, devs[0].ID, store.DeviceTrusted, &h.empID)
	h.eng.DeviceChanged(h.empID, devs[0].ID, store.DeviceTrusted)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
}

func TestProxyStrictBlocksNewDevice(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStrict)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusForbidden, "device_unconfirmed")
	h.eng.Flush()
	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	if len(devs) != 1 || devs[0].Status != store.DevicePending {
		t.Fatalf("strict mode should still register the device as pending: %+v", devs)
	}
}

func TestProxyOutdatedNoticeVersion(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	s := h.set.Get(h.ctx)
	s.NoticeText += "（更新）"
	if err := h.set.Put(h.ctx, s); err != nil {
		t.Fatal(err)
	}
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusForbidden, "notice_required")
}

func TestProxyDBDownReturns503NotInvalidKey(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	h.st.Pool.Close()
	w := doReq(t, h.r, h.key, codexBody)
	wantCode(t, w, http.StatusServiceUnavailable, "gateway_unavailable")
}
