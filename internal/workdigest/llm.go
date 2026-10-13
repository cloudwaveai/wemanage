package workdigest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"aiseat/internal/proxy"
	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// llm asks one ChatGPT account, through CPA's chat completions, to write
// prose. Requests carry the identity the gateway gives that account's
// non-Codex traffic, so they look like the rest of it upstream.
type llm struct {
	baseURL string // CPA, e.g. http://cpa:8317
	apiKey  string // CPA client key
	model   string // "acct-N/<model>"
	profile settings.CodexProfile
	device  store.DeviceProfile
	hc      *http.Client
}

func (l llm) complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":  l.model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header = proxy.BuildUpstreamHeaders(proxy.HeaderInput{
		Kind: "work_digest", Client: http.Header{"Accept": {"application/json"}},
		Profile: l.profile, Device: l.device, SessionID: uuid.NewString(), CPAAPIKey: l.apiKey,
	})
	resp, err := l.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: status %d: %.300s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("llm: empty reply")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
