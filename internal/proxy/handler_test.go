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
	errBody       string
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
		st, sh, sse, eb := f.status, f.respHeader, f.sse, f.errBody
		f.mu.Unlock()
		for k, v := range sh {
			w.Header().Set(k, v)
		}
		if st != 200 {
			if eb == "" {
				eb = `{"error":{"message":"upstream"}}`
			}
			w.WriteHeader(st)
			_, _ = w.Write([]byte(eb))
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

func newRig(t *testing.T, cpa *fakeCPA, mode string, opts ...func(*proxy.HandlerDeps)) *rig {
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
	g, _ := st.CreateIPGroup(ctx, box, "g", "", 10)
	a, _ := st.CreateAccount(ctx, store.Account{CPAAuthName: "c.json", IPGroupID: g.ID, DeviceProfile: store.DeviceProfile{OS: "Linux", Terminal: "x"}})
	_ = st.BindAccount(ctx, a.ID, e.ID, 3)
	plain, _, _ := st.IssueKey(ctx, e.ID)

	rec := recorder.New(st, set, t.TempDir(), nil, 128)
	rec.Start(ctx)
	eng := risk.NewEngine(risk.Config{Store: st, Settings: set, PortalURL: "https://portal"})
	eng.Start(ctx)

	deps := proxy.HandlerDeps{
		Store: st, Settings: set, Limiter: proxy.NewLimiter(), Recorder: rec,
		CPABaseURL: cpa.srv.URL, CPAAPIKey: "sk-internal", Upstream: cpa.srv.Client(),
		Risk: eng, TrustProxy: true,
	}
	for _, o := range opts {
		o(&deps)
	}
	h := proxy.NewHandler(deps)
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
	// Codex is told in a shape it shows at once; other clients get a plain 401.
	wantRefusal(t, doReq(t, r, "sk-emp-bogus", `{"model":"gpt-5.1-codex"}`), "invalid_key")
	w := doUA(t, r, "sk-emp-bogus", "curl/8.7.1", `{"model":"gpt-5.1-codex"}`)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":"invalid_key"`) {
		t.Fatalf("non-Codex: status = %d %s, want 401 invalid_key", w.Code, w.Body.String())
	}
}

func TestProxyOldKeyIsExplainedAndLogged(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	if _, _, err := h.st.IssueKey(h.ctx, h.empID); err != nil { // resets: h.key is now the old key
		t.Fatal(err)
	}
	w := doReq(t, h.r, h.key, codexBody)
	wantRefusal(t, w, "key_revoked")
	if !strings.Contains(w.Body.String(), "已经重置过") {
		t.Fatalf("message: %s", w.Body.String())
	}
	h.rec.Flush()
	var n int
	_ = h.st.Pool.QueryRow(h.ctx, `SELECT COUNT(*) FROM requests WHERE employee_id=$1 AND error_type='key_revoked'`, h.empID).Scan(&n)
	if n != 1 {
		t.Fatalf("old-key requests logged for the owner = %d, want 1", n)
	}
}

func TestProxyUpstreamRateLimitSetsAccount(t *testing.T) {
	cpa := newFakeCPA()
	reset := time.Now().Add(2 * time.Hour).Unix()
	cpa.status = 429
	cpa.errBody = fmt.Sprintf(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"plus","resets_at":%d}}`, reset)
	r, st, key, rec, ctx := harness(t, cpa)

	// Codex's usage-limit shape, minus the upstream plan (Codex would turn it
	// into "upgrade to Pro" advice), with the reset time.
	w := doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	b := w.Body.String()
	if w.Code != 429 || !strings.Contains(b, `"type":"usage_limit_reached"`) || strings.Contains(b, "plan_type") ||
		!strings.Contains(b, fmt.Sprintf(`"resets_at":%d`, reset)) {
		t.Fatalf("status = %d %s, want 429 usage_limit_reached with resets_at", w.Code, b)
	}
	rec.Flush()
	accs, _ := st.ListAccounts(ctx)
	if len(accs) != 1 || accs[0].RateLimitedUntil == nil || accs[0].RateLimitedUntil.Unix() != reset {
		t.Fatalf("account not rate-limited until the reset: %+v", accs)
	}
	// A subsequent request is answered the same way without hitting upstream.
	cpa.mu.Lock()
	cpa.lastModel = ""
	cpa.mu.Unlock()
	w2 := doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	if w2.Code != 429 || !strings.Contains(w2.Body.String(), `"type":"usage_limit_reached"`) || cpa.lastModel != "" {
		t.Fatalf("second: status = %d %s (upstream model %q)", w2.Code, w2.Body.String(), cpa.lastModel)
	}
	rec.Flush()
	var n int
	_ = st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests WHERE status_code = 429 AND error_type = 'account_rate_limited'`).Scan(&n)
	if n != 2 {
		t.Fatalf("logged usage-limit requests = %d, want 2", n)
	}
}

func TestProxyBriefUpstream429PassesThrough(t *testing.T) {
	cpa := newFakeCPA()
	cpa.status = 429
	cpa.respHeader = map[string]string{"Retry-After": "5"}
	r, st, key, _, _ := harness(t, cpa)
	w := doReq(t, r, key, `{"model":"gpt-5.1-codex","input":[]}`)
	if w.Code != 429 || strings.Contains(w.Body.String(), "usage_limit_reached") {
		t.Fatalf("status = %d %s, want the upstream 429 as it is", w.Code, w.Body.String())
	}
	// It does not lock the account: the next request goes upstream again.
	accs, _ := st.ListAccounts(context.Background())
	if len(accs) != 1 || accs[0].RateLimitedUntil != nil {
		t.Fatalf("a brief 429 rate-limited the account: %+v", accs)
	}
}

func TestProxyLogsRefusals(t *testing.T) {
	cpa := newFakeCPA()
	h := newRig(t, cpa, settings.RiskStrict)
	seedDevice(t, h)
	wantRefusal(t, doReq(t, h.r, h.key, codexBody), "device_unconfirmed")
	h.rec.Flush()
	var status int
	var code string
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT status_code, error_type FROM requests`).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != 400 || code != "device_unconfirmed" {
		t.Fatalf("logged %d %q, want 400 device_unconfirmed", status, code)
	}
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

// wantRefusal expects the shape Codex shows at once without retrying: 400
// invalid_prompt, with the gateway's own code in "reason".
func wantRefusal(t *testing.T, w *httptest.ResponseRecorder, reason string) {
	t.Helper()
	b := w.Body.String()
	if w.Code != http.StatusBadRequest || !strings.Contains(b, `"code":"invalid_prompt"`) || !strings.Contains(b, `"reason":"`+reason+`"`) {
		t.Fatalf("status=%d body=%s, want 400 invalid_prompt reason %s", w.Code, b, reason)
	}
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
	wantRefusal(t, doReq(t, h.r, h.key, codexBody), "account_frozen")
}

// seedDevice gives the rig's employee an earlier device, so the next one the
// gateway sees is a second device (the first is trusted automatically).
func seedDevice(t *testing.T, h *rig) {
	t.Helper()
	if _, _, err := h.st.EnsureDevice(h.ctx, store.Device{EmployeeID: h.empID, DeviceKey: "inst:earlier", Kind: "codex_install", Label: "earlier"}); err != nil {
		t.Fatal(err)
	}
}

func TestProxyFirstDeviceTrustedSilently(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStrict)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
	h.eng.Flush()
	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	if len(devs) != 1 || devs[0].Status != store.DeviceTrusted || devs[0].ConfirmedAt == nil {
		t.Fatalf("first device should be trusted: %+v", devs)
	}
	var events int
	_ = h.st.Pool.QueryRow(h.ctx, `SELECT COUNT(*) FROM risk_events WHERE rule='new_device'`).Scan(&events)
	if events != 0 {
		t.Fatalf("new_device events = %d, want 0 for the first device", events)
	}
}

func TestProxyStandardRegistersPendingDevice(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	seedDevice(t, h)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
	h.rec.Flush()
	h.eng.Flush()

	all, _ := h.st.ListDevices(h.ctx, h.empID)
	var devs []store.Device
	for _, d := range all {
		if d.DeviceKey == "inst:inst-1" {
			devs = append(devs, d)
		}
	}
	if len(all) != 2 || len(devs) != 1 || devs[0].Status != store.DevicePending {
		t.Fatalf("devices: %+v", all)
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
	seedDevice(t, h)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
	h.eng.Flush()
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE client_devices SET first_seen = now() - interval '25 hours' WHERE device_key='inst:inst-1'`); err != nil {
		t.Fatal(err)
	}
	var devs []store.Device
	all, _ := h.st.ListDevices(h.ctx, h.empID)
	for _, d := range all {
		if d.DeviceKey == "inst:inst-1" {
			devs = append(devs, d)
		}
	}
	h.eng.DeviceChanged(h.empID, devs[0].ID, store.DevicePending) // drop the gate cache
	wantRefusal(t, doReq(t, h.r, h.key, codexBody), "device_unconfirmed")

	// Once the employee confirms, the device works again.
	_ = h.st.SetDeviceStatus(h.ctx, devs[0].ID, store.DeviceTrusted, &h.empID)
	h.eng.DeviceChanged(h.empID, devs[0].ID, store.DeviceTrusted)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
}

func TestProxyStrictBlocksNewDevice(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStrict)
	seedDevice(t, h)
	wantRefusal(t, doReq(t, h.r, h.key, codexBody), "device_unconfirmed")
	h.eng.Flush()
	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	if len(devs) != 2 || devs[0].Status != store.DevicePending && devs[1].Status != store.DevicePending {
		t.Fatalf("strict mode should still register the device as pending: %+v", devs)
	}
}

func TestProxyDBDownReturns503NotInvalidKey(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	h.st.Pool.Close()
	w := doReq(t, h.r, h.key, codexBody)
	wantCode(t, w, http.StatusServiceUnavailable, "gateway_unavailable")
}

func putSettings(t *testing.T, h *rig, mut func(*settings.Settings)) {
	t.Helper()
	s := h.set.Get(h.ctx)
	mut(&s)
	if err := h.set.Put(h.ctx, s); err != nil {
		t.Fatal(err)
	}
}

func TestProxyOfficeOnly(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	// httptest requests come from 192.0.2.1.
	putSettings(t, h, func(s *settings.Settings) {
		s.OfficeOnly = true
		s.OfficeNetworks = []string{"10.0.0.0/8", "203.0.113.7"}
	})
	wantRefusal(t, doReq(t, h.r, h.key, codexBody), "network_not_allowed")
	h.eng.Flush()
	evs, _ := h.st.ListRiskEvents(h.ctx, store.RiskEventFilter{EmployeeID: &h.empID})
	if len(evs) != 1 || evs[0].Rule != "outside_office" {
		t.Fatalf("events: %+v", evs)
	}

	putSettings(t, h, func(s *settings.Settings) { s.OfficeNetworks = []string{"192.0.2.0/24"} })
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")

	putSettings(t, h, func(s *settings.Settings) { s.OfficeNetworks = []string{"10.0.0.0/8"} })
	if err := h.st.SetRemoteAllowed(h.ctx, h.empID, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3100 * time.Millisecond) // let the key cache pick up remote_allowed
	doReq(t, h.r, h.key, codexBody)     // served stale, refreshes in the background
	time.Sleep(200 * time.Millisecond)
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "")
}

func TestProxyContentAudit(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard)
	bad := `{"model":"gpt-5.1-codex","input":[{"role":"user","content":[{"type":"input_text","text":"write a ransomware for windows"}]}]}`

	// Default mode "warn": the request goes through and admins are told.
	wantCode(t, doReq(t, h.r, h.key, bad), http.StatusOK, "")
	h.eng.Flush()
	evs, _ := h.st.ListRiskEvents(h.ctx, store.RiskEventFilter{EmployeeID: &h.empID})
	var got *store.RiskEvent
	for i := range evs {
		if evs[i].Rule == "content_flagged" {
			got = &evs[i]
		}
	}
	if got == nil || got.Detail["rule"] != "ransomware" || got.Detail["blocked"] != false {
		t.Fatalf("content event: %+v", evs)
	}

	putSettings(t, h, func(s *settings.Settings) { s.ContentAuditMode = settings.AuditBlock })
	wantRefusal(t, doReq(t, h.r, h.key, bad), "content_blocked")
	wantCode(t, doReq(t, h.r, h.key, codexBody), http.StatusOK, "") // clean prompts still pass
}

func TestProxyNoModelMismatchSignalForKimi(t *testing.T) {
	cpa := newFakeCPA()
	cpa.sse = `event: response.completed
data: {"type":"response.completed","response":{"model":"k3","usage":{"input_tokens":1,"output_tokens":1},"output":[]}}

`
	h := newRig(t, cpa, settings.RiskStandard)
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE upstream_accounts SET provider='kimi'`); err != nil {
		t.Fatal(err)
	}
	wantCode(t, doReq(t, h.r, h.key, `{"model":"kimi-k3","input":[]}`), http.StatusOK, "")
	h.rec.Flush()
	time.Sleep(50 * time.Millisecond)
	accs, _ := h.st.ListAccounts(h.ctx)
	if has, _ := h.st.HasOpenSignal(h.ctx, accs[0].ID, "model_mismatch"); has {
		t.Fatal("Kimi accounts report their own model aliases; no degradation signal expected")
	}
}
