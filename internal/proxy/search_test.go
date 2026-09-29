package proxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aiseat/internal/cpa"
	"aiseat/internal/proxy"
	"aiseat/internal/settings"
)

// fakeSearch stands in for CPA's management API.
type fakeSearch struct {
	mu    sync.Mutex
	calls []cpa.APICallRequest
	lists int
}

func (f *fakeSearch) ListAuthFiles(context.Context) ([]cpa.AuthFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	a := cpa.AuthFile{Name: "c.json", AuthIndex: "idx-1"}
	a.IDToken.ChatGPTAccountID = "acct-uuid"
	return []cpa.AuthFile{a}, nil
}

func (f *fakeSearch) APICall(_ context.Context, call cpa.APICallRequest) (cpa.APICallResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return cpa.APICallResponse{StatusCode: 200,
		Body: `{"output":"results","results":[{"type":"text_result","url":"https://example.com/a"},{"type":"text_result","url":"https://example.com/b"}]}`}, nil
}

const searchBody = `{"id":"sess-1","model":"gpt-6-luna","commands":{"search_query":[{"q":"codex release notes"}]},"settings":{"allowed_callers":["direct"]}}`

func searchReq(t *testing.T, r http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.159.0 (Mac OS 26.2.0; arm64) Apple_Terminal/455.1")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("X-Openai-Actor-Authorization", "aiseat")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSearchGoesThroughCPAForTheAccount(t *testing.T) {
	f := &fakeSearch{}
	h := newRig(t, newFakeCPA(), settings.RiskStandard, func(d *proxy.HandlerDeps) { d.CPA = f })
	w := searchReq(t, h.r, h.key, searchBody)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "example.com/a") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	searchReq(t, h.r, h.key, searchBody)
	f.mu.Lock()
	call, lists, n := f.calls[0], f.lists, len(f.calls)
	f.mu.Unlock()
	if n != 2 || lists != 1 {
		t.Fatalf("calls %d, auth-file listings %d (want 2 and 1: the list is cached)", n, lists)
	}
	if call.AuthIndex != "idx-1" || call.Method != "POST" || call.URL != "https://chatgpt.com/backend-api/codex/alpha/search" {
		t.Fatalf("call: %+v", call)
	}
	if call.Header["Authorization"] != "Bearer $TOKEN$" || call.Header["ChatGPT-Account-Id"] != "acct-uuid" ||
		call.Header["Originator"] != "codex_cli_rs" || !strings.HasPrefix(call.Header["User-Agent"], "codex_cli_rs/") {
		t.Fatalf("headers: %+v", call.Header)
	}
	if _, leaked := call.Header["X-Openai-Actor-Authorization"]; leaked {
		t.Fatal("the tools header must not go upstream")
	}
	if call.Data != searchBody {
		t.Fatalf("body not passed through: %s", call.Data)
	}

	h.rec.Flush()
	var endpoint, model string
	var status int
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT endpoint, model_requested, status_code FROM requests ORDER BY started_at LIMIT 1`).
		Scan(&endpoint, &model, &status); err != nil {
		t.Fatal(err)
	}
	if endpoint != "search" || model != "gpt-6-luna" || status != 200 {
		t.Fatalf("logged %s %s %d", endpoint, model, status)
	}
	rows, _ := h.st.Pool.Query(h.ctx, `SELECT text FROM request_texts`)
	var texts []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		texts = append(texts, s)
	}
	rows.Close()
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "[联网搜索] codex release notes") || !strings.Contains(joined, "搜索到 2 条结果") {
		t.Fatalf("logged texts: %s", joined)
	}
}

func TestSearchNeedsAChatGPTAccount(t *testing.T) {
	h := newRig(t, newFakeCPA(), settings.RiskStandard, func(d *proxy.HandlerDeps) { d.CPA = &fakeSearch{} })
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE upstream_accounts SET provider='kimi'`); err != nil {
		t.Fatal(err)
	}
	w := searchReq(t, h.r, h.key, searchBody)
	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusBadRequest || !strings.Contains(string(body), `"reason":"search_unsupported"`) {
		t.Fatalf("kimi: %d %s", w.Code, body)
	}
}
