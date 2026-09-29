package risk_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/devicecard"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

type fakeMsg struct {
	mu   sync.Mutex
	sent map[string][]string
}

func (f *fakeMsg) SendText(_ context.Context, openID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sent == nil {
		f.sent = map[string][]string{}
	}
	f.sent[openID] = append(f.sent[openID], text)
	return nil
}

func (f *fakeMsg) to(openID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent[openID]...)
}

type env struct {
	st  *store.Store
	set *settings.Service
	eng *risk.Engine
	msg *fakeMsg
	emp store.Employee
	ctx context.Context
}

func newEnv(t *testing.T, mode string) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	set := settings.NewService(st)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := settings.Defaults()
	s.RiskMode = mode
	if err := set.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_emp", Name: "张三"})
	if err := st.EnsureSuperAdmin(ctx, "ou_admin", "admin"); err != nil {
		t.Fatal(err)
	}
	msg := &fakeMsg{}
	eng := risk.NewEngine(risk.Config{Store: st, Settings: set, Messenger: msg, PortalURL: "https://portal"})
	eng.Start(ctx)
	return &env{st: st, set: set, eng: eng, msg: msg, emp: emp, ctx: ctx}
}

// device registers a device and returns its gate view with an adjustable age.
func (e *env) device(t *testing.T, key string, age time.Duration, status string) risk.CachedDevice {
	t.Helper()
	d, _, err := e.st.EnsureDevice(e.ctx, store.Device{EmployeeID: e.emp.ID, DeviceKey: key, Kind: "ua", Label: key})
	if err != nil {
		t.Fatal(err)
	}
	// Set it explicitly: an employee's first device is created trusted.
	_ = e.st.SetDeviceStatus(e.ctx, d.ID, status, nil)
	return risk.CachedDevice{ID: d.ID, Status: status, FirstSeen: time.Now().Add(-age), Label: key}
}

func (e *env) obs(dev risk.CachedDevice, ip string) risk.Observation {
	return risk.Observation{EmployeeID: e.emp.ID, OpenID: "ou_emp", Device: dev, IP: ip, At: time.Now(), ClientKind: "codex"}
}

func (e *env) events(t *testing.T, rule string) []store.RiskEvent {
	t.Helper()
	evs, err := e.st.ListRiskEvents(e.ctx, store.RiskEventFilter{EmployeeID: &e.emp.ID})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.RiskEvent
	for _, ev := range evs {
		if ev.Rule == rule {
			out = append(out, ev)
		}
	}
	return out
}

func (e *env) frozen(t *testing.T) bool {
	t.Helper()
	emp, err := e.st.GetEmployee(e.ctx, e.emp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return emp.FrozenAt != nil
}

func TestEngineNewDeviceNotifiesEmployee(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	dev := e.device(t, "laptop", 0, store.DevicePending)
	o := e.obs(dev, "1.2.3.4")
	o.NewDevice = true
	e.eng.Observe(o)
	e.eng.Flush()

	if len(e.events(t, risk.RuleNewDevice)) != 1 {
		t.Fatal("expected new_device event")
	}
	got := e.msg.to("ou_emp")
	if len(got) != 1 || !strings.Contains(got[0], "新设备") || !strings.Contains(got[0], "https://portal/devices") {
		t.Fatalf("employee message: %q", got)
	}
	d, _ := e.st.GetDevice(e.ctx, dev.ID)
	if d.NotifiedAt == nil {
		t.Fatal("device not marked notified")
	}
}

func TestEngineConcurrentUntrustedFreezes(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	mine := e.device(t, "mine", 48*time.Hour, store.DeviceTrusted)
	buyer := e.device(t, "buyer", 2*time.Hour, store.DevicePending)

	e.eng.Observe(e.obs(mine, "1.2.3.4"))
	e.eng.Observe(e.obs(buyer, "9.9.9.9"))
	e.eng.Flush()

	if !e.frozen(t) {
		t.Fatal("employee should be frozen")
	}
	evs := e.events(t, risk.RuleConcurrentUntrusted)
	if len(evs) != 1 || evs[0].Severity != "high" || evs[0].DeviceID == nil || *evs[0].DeviceID != buyer.ID {
		t.Fatalf("events: %+v", evs)
	}
	if len(e.msg.to("ou_admin")) == 0 || len(e.msg.to("ou_emp")) == 0 {
		t.Fatal("admin and employee should both be told about the freeze")
	}

	// After an admin unfreeze the stale window must not re-freeze immediately.
	_ = e.st.UnfreezeEmployee(e.ctx, e.emp.ID)
	e.eng.Unfrozen(e.emp.ID)
	e.eng.Observe(e.obs(mine, "1.2.3.4"))
	e.eng.Flush()
	if e.frozen(t) {
		t.Fatal("re-frozen from stale sightings after unfreeze")
	}
}

func TestEngineGracePeriodAndSameNetwork(t *testing.T) {
	// A just-registered device elsewhere is inside its grace period.
	e := newEnv(t, settings.RiskStandard)
	mine := e.device(t, "mine", 48*time.Hour, store.DeviceTrusted)
	fresh := e.device(t, "fresh", 5*time.Minute, store.DevicePending)
	e.eng.Observe(e.obs(mine, "1.2.3.4"))
	e.eng.Observe(e.obs(fresh, "9.9.9.9"))
	e.eng.Flush()
	if e.frozen(t) {
		t.Fatalf("grace period: should not freeze: %+v", e.events(t, risk.RuleConcurrentUntrusted))
	}

	// An unconfirmed device on the same /16 (office NAT pool) is not "elsewhere".
	e = newEnv(t, settings.RiskStandard)
	mine = e.device(t, "mine", 48*time.Hour, store.DeviceTrusted)
	office := e.device(t, "office", 3*time.Hour, store.DevicePending)
	e.eng.Observe(e.obs(mine, "1.2.3.4"))
	e.eng.Observe(e.obs(office, "1.2.8.8"))
	e.eng.Flush()
	if e.frozen(t) {
		t.Fatalf("same network: should not freeze: %+v", e.events(t, risk.RuleConcurrentUntrusted))
	}
}

func TestEngineMultiNetworkFreezesEvenTrusted(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	a := e.device(t, "a", 48*time.Hour, store.DeviceTrusted)
	b := e.device(t, "b", 48*time.Hour, store.DeviceTrusted)
	c := e.device(t, "c", 48*time.Hour, store.DeviceTrusted)
	e.eng.Observe(e.obs(a, "1.2.3.4"))
	e.eng.Observe(e.obs(b, "5.6.7.8"))
	e.eng.Observe(e.obs(c, "9.10.11.12"))
	e.eng.Flush()
	if !e.frozen(t) || len(e.events(t, risk.RuleMultiNetwork)) != 1 {
		t.Fatal("multi_network should freeze")
	}
}

func TestEngineLaxModeOnlyNotifies(t *testing.T) {
	e := newEnv(t, settings.RiskLax)
	mine := e.device(t, "mine", 48*time.Hour, store.DeviceTrusted)
	buyer := e.device(t, "buyer", 2*time.Hour, store.DevicePending)
	e.eng.Observe(e.obs(mine, "1.2.3.4"))
	e.eng.Observe(e.obs(buyer, "9.9.9.9"))
	e.eng.Flush()
	if e.frozen(t) {
		t.Fatal("lax mode must not freeze")
	}
	if len(e.events(t, risk.RuleConcurrentUntrusted)) != 1 || len(e.msg.to("ou_admin")) == 0 {
		t.Fatal("lax mode should still record and notify admins")
	}
}

func TestEngineNonCodexBlockedDeduped(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	for i := 0; i < 3; i++ {
		e.eng.Observe(risk.Observation{EmployeeID: e.emp.ID, OpenID: "ou_emp", IP: "1.2.3.4", At: time.Now(),
			ClientKind: "other", UserAgent: "curl/8", Denied: "client_not_allowed"})
	}
	e.eng.Flush()
	if n := len(e.events(t, risk.RuleNonCodexBlocked)); n != 1 {
		t.Fatalf("non_codex_blocked events = %d, want 1", n)
	}
	if n := len(e.msg.to("ou_emp")); n != 1 {
		t.Fatalf("employee messages = %d, want 1", n)
	}
}

func TestEngineExpirePending(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	dev := e.device(t, "stale", 0, store.DevicePending)
	if _, err := e.st.Pool.Exec(e.ctx, `UPDATE client_devices SET first_seen = now() - interval '25 hours' WHERE id=$1`, dev.ID); err != nil {
		t.Fatal(err)
	}
	keep := e.device(t, "recent", 0, store.DevicePending)

	if err := e.eng.ExpirePending(e.ctx); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.st.GetDevice(e.ctx, dev.ID); d.Status != store.DeviceExpired {
		t.Fatalf("stale device status = %s", d.Status)
	}
	if d, _ := e.st.GetDevice(e.ctx, keep.ID); d.Status != store.DevicePending {
		t.Fatalf("recent device status = %s", d.Status)
	}
	if got := e.msg.to("ou_emp"); len(got) != 1 || !strings.Contains(got[0], "暂停") {
		t.Fatalf("expiry message: %q", got)
	}
}

func TestEngineAccumulatesDeviceAndNetworkUses(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	dev := e.device(t, "laptop", 48*time.Hour, store.DeviceTrusted)
	for i, ip := range []string{"1.2.3.4", "1.2.3.5", "1.2.3.6"} {
		o := e.obs(dev, ip)
		o.At = time.Now().Add(time.Duration(i) * time.Second)
		e.eng.Observe(o)
	}
	e.eng.Flush()

	d, err := e.st.GetDevice(e.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Requests != 3 || d.LastIP != "1.2.3.6" {
		t.Fatalf("device requests=%d last_ip=%s, want 3 / 1.2.3.6", d.Requests, d.LastIP)
	}
	nets, _ := e.st.ListNetworks(e.ctx, e.emp.ID, 10)
	if len(nets) != 1 || nets[0].Requests != 3 || nets[0].IPSample != "1.2.3.6" {
		t.Fatalf("networks = %+v, want one /24 with 3 uses", nets)
	}
}

func TestEngineTellsAdminsAboutKeyWithoutAccount(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	o := e.obs(risk.CachedDevice{}, "1.2.3.4")
	o.Denied = "no_account_bound"
	e.eng.Observe(o)
	e.eng.Observe(o) // once a day is enough
	e.eng.Flush()
	got := e.msg.to("ou_admin")
	if len(got) != 1 || !strings.Contains(got[0], "张三") || !strings.Contains(got[0], "https://portal/accounts") {
		t.Fatalf("admin messages: %q", got)
	}
	if n := len(e.msg.to("ou_emp")); n != 0 {
		t.Fatalf("employee got %d messages; Codex already tells them", n)
	}
}

type fakeCardMsg struct {
	fakeMsg
	cards map[string][]any
}

func (f *fakeCardMsg) SendCard(_ context.Context, openID string, card any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cards == nil {
		f.cards = map[string][]any{}
	}
	f.cards[openID] = append(f.cards[openID], card)
	return nil
}

func TestEngineAsksWithCardOnceCallbacksWork(t *testing.T) {
	e := newEnv(t, settings.RiskStandard)
	box, _ := secure.NewBox(make([]byte, 32))
	msg := &fakeCardMsg{}
	eng := risk.NewEngine(risk.Config{Store: e.st, Settings: e.set, Messenger: msg, PortalURL: "https://portal",
		Cards: &devicecard.Signer{Box: box}})
	eng.Start(e.ctx)

	newDevice := func(key string) {
		o := e.obs(e.device(t, key, 0, store.DevicePending), "1.2.3.4")
		o.NewDevice = true
		eng.Observe(o)
		eng.Flush()
	}
	// Until Feishu has checked the callback URL, buttons would not work: text.
	newDevice("laptop-a")
	if len(msg.to("ou_emp")) != 1 || len(msg.cards["ou_emp"]) != 0 {
		t.Fatalf("before callbacks: texts %d cards %d", len(msg.to("ou_emp")), len(msg.cards["ou_emp"]))
	}
	if err := e.st.MarkCardCallbackReady(e.ctx); err != nil {
		t.Fatal(err)
	}
	newDevice("laptop-b")
	if len(msg.to("ou_emp")) != 1 || len(msg.cards["ou_emp"]) != 1 {
		t.Fatalf("after callbacks: texts %d cards %d", len(msg.to("ou_emp")), len(msg.cards["ou_emp"]))
	}
}
