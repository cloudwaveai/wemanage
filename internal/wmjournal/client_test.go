package wmjournal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelClientUsesConfiguredModelAndReportsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-key" {
			t.Error("incorrect request")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "mock-sol" || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "证据") {
			t.Error("incorrect body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"mock-sol","choices":[{"message":{"content":"项目工作记录"}}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`))
	}))
	defer srv.Close()
	c := Client{URL: srv.URL + "/v1", Key: "local-test-key", Model: "mock-sol", HTTP: srv.Client()}
	text, model, usage, err := c.Extract(context.Background(), "证据")
	if err != nil || text != "项目工作记录" || model != "mock-sol" || usage.Input == nil || *usage.Input != 12 || usage.Output == nil || *usage.Output != 4 {
		t.Fatalf("unexpected result %q %q %+v %v", text, model, usage, err)
	}
}
