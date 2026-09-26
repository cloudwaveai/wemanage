package report_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiseat/internal/report"
)

func TestLLMComplete(t *testing.T) {
	var gotAuth, gotPath, gotModel, gotSystem, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = req.Model
		for _, m := range req.Messages {
			switch m.Role {
			case "system":
				gotSystem = m.Content
			case "user":
				gotUser = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"这是固定回复"}}]}`))
	}))
	defer srv.Close()

	l := report.LLM{BaseURL: srv.URL, APIKey: "secret-key", Model: "gpt-x"}
	got, err := l.Complete(context.Background(), "sys-prompt", "user-prompt")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "这是固定回复" {
		t.Errorf("content = %q, want %q", got, "这是固定回复")
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth = %q, want Bearer secret-key", gotAuth)
	}
	if gotModel != "gpt-x" {
		t.Errorf("model = %q, want gpt-x", gotModel)
	}
	if gotSystem != "sys-prompt" || gotUser != "user-prompt" {
		t.Errorf("messages = (%q,%q), want (sys-prompt,user-prompt)", gotSystem, gotUser)
	}
}

func TestLLMCompleteNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream boom"))
	}))
	defer srv.Close()

	l := report.LLM{BaseURL: srv.URL, Model: "gpt-x"}
	_, err := l.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected error for non-2xx, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should include status 500", err.Error())
	}
	if !strings.Contains(err.Error(), "upstream boom") {
		t.Errorf("error %q should include body", err.Error())
	}
}
