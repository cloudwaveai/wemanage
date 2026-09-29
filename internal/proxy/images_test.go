package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aiseat/internal/settings"
)

// fakeImages is a CPA stand-in for the Images API.
type fakeImages struct {
	mu      sync.Mutex
	model   string
	actor   string
	ua      string
	status  int
	prompts []string
}

func (f *fakeImages) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.model, _ = body["model"].(string)
		f.actor = r.Header.Get("X-Openai-Actor-Authorization")
		f.ua = r.Header.Get("User-Agent")
		p, _ := body["prompt"].(string)
		f.prompts = append(f.prompts, p)
		status := f.status
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 0 && status != 200 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"image quota"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"created":1,"size":"1024x1024","quality":"low","output_format":"png",
			"data":[{"b64_json":"` + strings.Repeat("A", 4096) + `","revised_prompt":"a blue circle"}],
			"usage":{"input_tokens":12,"output_tokens":1056,"total_tokens":1068}}`))
	})
}

func imageReq(t *testing.T, h http.Handler, key, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex-tui/0.158.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.158.0)")
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("X-Openai-Actor-Authorization", "aiseat")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestImagesGenerationProxied(t *testing.T) {
	f := &fakeImages{}
	h := newRig(t, &fakeCPA{srv: httptest.NewServer(f.handler())}, settings.RiskStandard)

	w := imageReq(t, h.r, h.key, "/v1/images/generations",
		`{"model":"gpt-image-1.5","prompt":"a small blue circle","size":"1024x1024","quality":"low"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "b64_json") {
		t.Fatalf("status %d body %.200s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	model, actor, ua := f.model, f.actor, f.ua
	f.mu.Unlock()
	if !strings.HasPrefix(model, "acct-") || !strings.HasSuffix(model, "/gpt-image-1.5") {
		t.Fatalf("upstream model = %q", model)
	}
	if actor != "" {
		t.Fatalf("x-openai-actor-authorization leaked upstream: %q", actor)
	}
	if !strings.HasPrefix(ua, "codex-tui/") {
		t.Fatalf("client UA not forwarded: %q", ua)
	}

	// The log keeps the prompt and a summary, never the image data.
	h.rec.Flush()
	var endpoint string
	var in, out int64
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT endpoint, tokens_input, tokens_output FROM requests`).Scan(&endpoint, &in, &out); err != nil {
		t.Fatal(err)
	}
	if endpoint != "images" || in != 12 || out != 1056 {
		t.Fatalf("logged %s in=%d out=%d", endpoint, in, out)
	}
	var texts []string
	rows, _ := h.st.Pool.Query(h.ctx, `SELECT role || ':' || text FROM request_texts ORDER BY role DESC`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		texts = append(texts, s)
	}
	rows.Close()
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "user:a small blue circle") || !strings.Contains(joined, "生成了 1 张图片") || strings.Contains(joined, "AAAA") {
		t.Fatalf("logged texts: %s", joined)
	}
	// No device is registered for image calls.
	if devs, _ := h.st.ListDevices(h.ctx, h.empID); len(devs) != 0 {
		t.Fatalf("image call registered devices: %+v", devs)
	}
}

func TestImagesQuotaDoesNotBlockChat(t *testing.T) {
	f := &fakeImages{status: http.StatusTooManyRequests}
	h := newRig(t, &fakeCPA{srv: httptest.NewServer(f.handler())}, settings.RiskStandard)
	w := imageReq(t, h.r, h.key, "/v1/images/generations", `{"model":"gpt-image-1.5","prompt":"x"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d", w.Code)
	}
	accs, _ := h.st.ListAccounts(h.ctx)
	if accs[0].RateLimitedUntil != nil || accs[0].NeedsLogin {
		t.Fatalf("an image 429 must not rate-limit the account: %+v", accs[0].Account)
	}
}

func TestImagesNeedChatGPTAndJSON(t *testing.T) {
	f := &fakeImages{}
	h := newRig(t, &fakeCPA{srv: httptest.NewServer(f.handler())}, settings.RiskStandard)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader("--x"))
	req.Header.Set("Authorization", "Bearer "+h.key)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("multipart: status %d", w.Code)
	}

	// A fresh rig, so the key cache has not seen the account as ChatGPT yet.
	h = newRig(t, &fakeCPA{srv: httptest.NewServer(f.handler())}, settings.RiskStandard)
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE upstream_accounts SET provider='kimi'`); err != nil {
		t.Fatal(err)
	}
	w = imageReq(t, h.r, h.key, "/v1/images/generations", `{"prompt":"x"}`)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "images_unsupported") {
		t.Fatalf("kimi: status %d %s", w.Code, w.Body.String())
	}
}
