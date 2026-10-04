package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// prefixCPA answers per account prefix: accounts in usedUp say their quota is
// used up, the others stream a normal response.
type prefixCPA struct {
	mu     sync.Mutex
	usedUp map[string]bool
	models []string // ids for /v1/models, with prefixes
	seen   []string // models requested, in order
}

func (p *prefixCPA) fake() *fakeCPA {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model := gjsonModel(body)
		prefix, _, _ := strings.Cut(model, "/")
		p.mu.Lock()
		p.seen = append(p.seen, model)
		out := p.usedUp[prefix]
		p.mu.Unlock()
		if out {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","resets_in_seconds":3600}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, defaultSSE)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		data := []map[string]any{}
		for i, id := range p.models {
			data = append(data, map[string]any{"id": id, "created": i})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	})
	return &fakeCPA{srv: httptest.NewServer(mux)}
}

func (p *prefixCPA) requested() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// twoAccounts gives the rig's employee a second account, and returns both.
func twoAccounts(t *testing.T, h *rig) (store.Account, store.Account) {
	t.Helper()
	accs, _ := h.st.AccountsOf(h.ctx, h.empID)
	a1 := accs[0]
	a2, err := h.st.CreateAccount(h.ctx, store.Account{CPAAuthName: "c2.json", IPGroupID: a1.IPGroupID,
		DeviceProfile: store.DeviceProfile{OS: "Linux", Terminal: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.BindAccount(h.ctx, a2.ID, h.empID, 3); err != nil {
		t.Fatal(err)
	}
	return a1, a2
}

func TestUsedUpAccountHandsOverToTheNext(t *testing.T) {
	p := &prefixCPA{usedUp: map[string]bool{}}
	h := newRig(t, p.fake(), settings.RiskLax)
	a1, a2 := twoAccounts(t, h)
	p.mu.Lock()
	p.usedUp[a1.Prefix()] = true
	p.mu.Unlock()

	w := doReq(t, h.r, h.key, `{"model":"gpt-5.1-codex","stream":true,"input":"hi"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatalf("expected the second account to answer, got %d %s", w.Code, w.Body.String())
	}
	got := p.requested()
	if len(got) != 2 || got[0] != a1.Prefix()+"/gpt-5.1-codex" || got[1] != a2.Prefix()+"/gpt-5.1-codex" {
		t.Fatalf("upstream calls = %v", got)
	}
	acc, _ := h.st.GetAccount(h.ctx, a1.ID)
	if acc.RateLimitedUntil == nil || !acc.RateLimitedUntil.After(time.Now()) {
		t.Fatal("the used-up account should be marked rate limited")
	}

	// The next request goes straight to the account that still has quota.
	w = doReq(t, h.r, h.key, `{"model":"gpt-5.1-codex","stream":true,"input":"again"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("second request: %d %s", w.Code, w.Body.String())
	}
	if got := p.requested(); len(got) != 3 || got[2] != a2.Prefix()+"/gpt-5.1-codex" {
		t.Fatalf("upstream calls = %v", got)
	}

	// With both used up, Codex gets the usage-limit answer.
	p.mu.Lock()
	p.usedUp[a2.Prefix()] = true
	p.mu.Unlock()
	w = doReq(t, h.r, h.key, `{"model":"gpt-5.1-codex","stream":true,"input":"third"}`)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "usage_limit_reached") {
		t.Fatalf("all accounts used up: %d %s", w.Code, w.Body.String())
	}
}

func TestModelPolicyPerAccount(t *testing.T) {
	p := &prefixCPA{usedUp: map[string]bool{}}
	h := newRig(t, p.fake(), settings.RiskLax)
	accs, _ := h.st.AccountsOf(h.ctx, h.empID)
	a1 := accs[0]
	if err := h.st.SetAccountModels(h.ctx, a1.ID, []string{"gpt-6-luna"}); err != nil {
		t.Fatal(err)
	}

	w := doReq(t, h.r, h.key, `{"model":"gpt-6-astra","stream":true,"input":"hi"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model_not_allowed") ||
		!strings.Contains(w.Body.String(), "gpt-6-luna") {
		t.Fatalf("disallowed model: %d %s", w.Code, w.Body.String())
	}
	if len(p.requested()) != 0 {
		t.Fatal("a disallowed model must not reach upstream")
	}
	w = doReq(t, h.r, h.key, `{"model":"gpt-6-luna","stream":true,"input":"hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("allowed model: %d %s", w.Code, w.Body.String())
	}
}

func TestModelPolicyRoutesToTheAccountThatAllowsIt(t *testing.T) {
	p := &prefixCPA{usedUp: map[string]bool{}}
	h := newRig(t, p.fake(), settings.RiskLax)
	a1, a2 := twoAccounts(t, h)
	_ = h.st.SetAccountModels(h.ctx, a1.ID, []string{"gpt-6-luna"})

	w := doReq(t, h.r, h.key, `{"model":"gpt-6-astra","stream":true,"input":"hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("astra on the open account: %d %s", w.Code, w.Body.String())
	}
	if got := p.requested(); len(got) != 1 || got[0] != a2.Prefix()+"/gpt-6-astra" {
		t.Fatalf("upstream calls = %v", got)
	}
}

func TestThirdPartyClientsNeedTheAccountOpened(t *testing.T) {
	p := &prefixCPA{usedUp: map[string]bool{}}
	h := newRig(t, p.fake(), settings.RiskLax)
	accs, _ := h.st.AccountsOf(h.ctx, h.empID)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6-luna","stream":true,"input":"hi"}`))
		req.Header.Set("Authorization", "Bearer "+h.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "opencode/1.4.0 ai-sdk/openai/2.0")
		w := httptest.NewRecorder()
		h.r.ServeHTTP(w, req)
		return w
	}
	if w := send(); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "client_not_allowed") {
		t.Fatalf("closed account: %d %s", w.Code, w.Body.String())
	}
	on := true
	_ = h.st.UpdateAccount(h.ctx, accs[0].ID, store.AccountPatch{AllowThirdParty: &on})
	// The key's principal is cached (3s, then refreshed in the background):
	// the change lands within a few seconds.
	var w *httptest.ResponseRecorder
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if w = send(); w.Code == http.StatusOK {
			break
		}
	}
	if w.Code != http.StatusOK {
		t.Fatalf("opened account: %d %s", w.Code, w.Body.String())
	}
}

func TestModelsListFollowsThePolicy(t *testing.T) {
	p := &prefixCPA{usedUp: map[string]bool{}}
	h := newRig(t, p.fake(), settings.RiskLax)
	a1, a2 := twoAccounts(t, h)
	p.models = []string{a1.Prefix() + "/gpt-6-luna", a1.Prefix() + "/gpt-6-astra", a2.Prefix() + "/gpt-6-sol", "acct-99/other"}
	_ = h.st.SetAccountModels(h.ctx, a1.ID, []string{"gpt-6-luna"})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+h.key)
	req.Header.Set("User-Agent", "codex-tui/0.157.1 (Linux; x64) x (codex-tui; 0.157.1)")
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var got struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	ids := []string{}
	for _, m := range got.Data {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "gpt-6-sol,gpt-6-luna" {
		t.Fatalf("models = %v (%s)", ids, w.Body.String())
	}
}
