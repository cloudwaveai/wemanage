package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// LLM is a separately-configured OpenAI-compatible chat endpoint used only to
// write prose. It is NOT an employee subscription account.
type LLM struct {
	BaseURL string
	APIKey  string
	Model   string
	HC      *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Complete calls {BaseURL}/chat/completions with a system+user message and
// returns the assistant content.
func (l LLM) Complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: l.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	url := strings.TrimRight(l.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if l.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}

	hc := l.HC
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: do request: %w", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}

	var out chatResponse
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: response had no choices")
	}
	return out.Choices[0].Message.Content, nil
}
