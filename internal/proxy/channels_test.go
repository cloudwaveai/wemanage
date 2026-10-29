package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"aiseat/internal/recorder"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"github.com/go-chi/chi/v5"
)

type channelRoundTrip func(*http.Request) (*http.Response, error)

func (f channelRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestChannelRoutesAndPreservesSeparateCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	set := settings.NewService(st)
	svc, err := st.CreateServiceAccount(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	rec := recorder.New(st, set, t.TempDir(), nil, 32)
	rec.Start(ctx)
	defer rec.Flush()
	h := NewHandler(HandlerDeps{Store: st, Settings: set, Box: box, Recorder: rec, Limiter: NewLimiter()})
	var forwarded bool
	h.channelHTTP = &http.Client{Transport: channelRoundTrip(func(r *http.Request) (*http.Response, error) {
		forwarded = true
		if r.URL.Host != "api.example.com" || r.Header.Get("Authorization") != "Bearer upstream-test-secret" {
			t.Errorf("wrong upstream target/auth")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"model":"m","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))}, nil
	})}
	ch, err := st.CreateAPIChannel(ctx, store.APIChannel{Name: "test", Protocol: "openai", BaseURL: "https://api.example.com/v1", CredentialCipher: box.Seal([]byte("upstream-test-secret"))})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := st.IssueServiceKey(ctx, svc.ID, "app", &ch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	h.Routes(router)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"vendor/m","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 200 || !forwarded {
		t.Fatalf("request status %d body %s", res.Code, res.Body.String())
	}
	rec.Flush()
	items, _, err := st.ListRequests(ctx, store.RequestFilter{EmployeeIDs: []int64{svc.ID}})
	if err != nil || len(items) != 1 || items[0].TokensInput != 3 || items[0].TokensOutput != 2 || items[0].ModelRequested != "vendor/m" {
		t.Fatalf("record: %+v %v", items, err)
	}
	forwarded = false
	mismatch := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"m","messages":[]}`))
	mismatch.Header.Set("x-api-key", key)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, mismatch)
	if res.Code != 400 || forwarded {
		t.Fatalf("protocol mismatch: %d", res.Code)
	}
	if _, err := st.SetAPIChannelActive(ctx, ch.ID, false); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req.Clone(ctx))
	if res.Code != 503 {
		t.Fatalf("disabled channel: %d", res.Code)
	}
}
func TestChannelAddressRules(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{{"127.0.0.1", false}, {"10.1.1.1", false}, {"169.254.169.254", false}, {"100.64.0.1", false}, {"::1", false}, {"8.8.8.8", true}} {
		if got := publicAddress(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Fatalf("%s=%v", tc.ip, got)
		}
	}
	for _, u := range []string{"http://example.com/v1", "https://a:b@example.com/v1", "https://example.com/v1?key=x"} {
		if ValidChannelURL(u) {
			t.Fatalf("accepted %s", u)
		}
	}
	if !ValidChannelURL("https://example.com/v1") {
		t.Fatal("valid https rejected")
	}
	_ = time.Second
}

func TestAnthropicChannelUsesNativeProtocol(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	set := settings.NewService(st)
	svc, err := st.CreateServiceAccount(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	rec := recorder.New(st, set, t.TempDir(), nil, 32)
	rec.Start(ctx)
	defer rec.Flush()
	h := NewHandler(HandlerDeps{Store: st, Settings: set, Box: box, Recorder: rec, Limiter: NewLimiter()})
	h.channelHTTP = &http.Client{Transport: channelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "anthropic-test-secret" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected Anthropic routing or credentials")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"model":"m","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":5,"output_tokens":2}}`))}, nil
	})}
	ch, err := st.CreateAPIChannel(ctx, store.APIChannel{Name: "test", Protocol: "anthropic", BaseURL: "https://api.example.com/v1", CredentialCipher: box.Seal([]byte("anthropic-test-secret"))})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := st.IssueServiceKey(ctx, svc.ID, "app", &ch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	h.Routes(router)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", key)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("status %d body %s", res.Code, res.Body.String())
	}
	rec.Flush()
	items, _, err := st.ListRequests(ctx, store.RequestFilter{EmployeeIDs: []int64{svc.ID}})
	if err != nil || len(items) != 1 || items[0].TokensInput != 5 || items[0].TokensOutput != 2 {
		t.Fatalf("record: %+v %v", items, err)
	}
}

func TestChannelRejectsRedirectInsteadOfFollowing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	set := settings.NewService(st)
	svc, err := st.CreateServiceAccount(ctx, "redirect app")
	if err != nil {
		t.Fatal(err)
	}
	rec := recorder.New(st, set, t.TempDir(), nil, 32)
	rec.Start(ctx)
	defer rec.Flush()
	h := NewHandler(HandlerDeps{Store: st, Settings: set, Box: box, Recorder: rec, Limiter: NewLimiter()})
	calls := 0
	h.channelHTTP = &http.Client{Transport: channelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://elsewhere.example/v1"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ch, err := st.CreateAPIChannel(ctx, store.APIChannel{Name: "redirect", Protocol: "openai", BaseURL: "https://example.com/v1", CredentialCipher: box.Seal([]byte("dummy"))})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := st.IssueServiceKey(ctx, svc.ID, "app", &ch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	h.Routes(router)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 502 || calls != 1 || res.Header().Get("Location") != "" {
		t.Fatalf("redirect status=%d calls=%d", res.Code, calls)
	}
}
