package proxy_test

import (
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

const claudeSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"model":"gpt-6-luna","usage":{"input_tokens":120,"cache_read_input_tokens":900}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好的"}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

// Claude Code talks Anthropic Messages with its key in x-api-key; the gateway
// routes it to the employee's account like any request and logs its usage.
func TestProxyClaudeCodeMessages(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotModel, gotAuth, gotAPIKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath, gotModel, gotAuth, gotAPIKey = r.URL.Path, gjsonModel(body), r.Header.Get("Authorization"), r.Header.Get("X-Api-Key")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, claudeSSE)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	})
	h := newRig(t, &fakeCPA{srv: httptest.NewServer(mux)}, settings.RiskStandard)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
			`{"model":"gpt-6-luna","stream":true,"max_tokens":32000,"metadata":{"user_id":"user_x_account_y_session_abc"},
			"system":[{"type":"text","text":"You are Claude Code"}],
			"messages":[{"role":"user","content":[{"type":"text","text":"修一下这个 bug"}]}]}`))
		req.Header.Set("X-Api-Key", h.key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "claude-cli/2.1.30 (external, cli)")
		w := httptest.NewRecorder()
		h.r.ServeHTTP(w, req)
		return w
	}

	// Claude Code is a third-party client: refused until the account allows it.
	if w := send(); w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "client_not_allowed") {
		t.Fatalf("third-party client on a Codex-only account: %d %s", w.Code, w.Body.String())
	}
	accs, _ := h.st.AccountsOf(h.ctx, h.empID)
	on := true
	_ = h.st.UpdateAccount(h.ctx, accs[0].ID, store.AccountPatch{AllowThirdParty: &on})

	// The key's principal is cached for a few seconds.
	var w *httptest.ResponseRecorder
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if w = send(); w.Code == http.StatusOK {
			break
		}
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("claude code request: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	if gotPath != "/v1/messages" || !strings.HasSuffix(gotModel, "/gpt-6-luna") || !strings.HasPrefix(gotModel, "acct-") ||
		gotAuth != "Bearer sk-internal" || gotAPIKey != "" {
		t.Fatalf("upstream: path %q model %q auth %q x-api-key %q", gotPath, gotModel, gotAuth, gotAPIKey)
	}
	mu.Unlock()

	h.rec.Flush()
	var endpoint string
	var in, cached, out int64
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT endpoint, tokens_input, tokens_cached, tokens_output FROM requests WHERE status_code=200`).
		Scan(&endpoint, &in, &cached, &out); err != nil {
		t.Fatal(err)
	}
	if endpoint != "messages" || in != 1020 || cached != 900 || out != 42 {
		t.Fatalf("logged %s in=%d cached=%d out=%d", endpoint, in, cached, out)
	}
}
