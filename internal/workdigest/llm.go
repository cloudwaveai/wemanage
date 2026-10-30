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

type ModelUsage struct {
	Input  *int64 `json:"input_tokens"`
	Output *int64 `json:"output_tokens"`
}

func (l llm) complete(ctx context.Context, system, user string) (string, error) {
	text, _, err := l.completeWithUsage(ctx, system, user)
	return strings.TrimSpace(text), err
}

func (l llm) completeWithUsage(ctx context.Context, system, user string) (string, ModelUsage, error) {
	body, err := json.Marshal(map[string]any{
		"model":  l.model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	if err != nil {
		return "", ModelUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", ModelUsage{}, err
	}
	req.Header = proxy.BuildUpstreamHeaders(proxy.HeaderInput{
		Kind: "work_digest", Client: http.Header{"Accept": {"application/json"}},
		Profile: l.profile, Device: l.device, SessionID: uuid.NewString(), CPAAPIKey: l.apiKey,
	})
	resp, err := l.hc.Do(req)
	if err != nil {
		return "", ModelUsage{}, fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return "", ModelUsage{}, fmt.Errorf("llm: read response: %w", err)
	}
	if len(data) > 4<<20 {
		return "", ModelUsage{}, fmt.Errorf("llm: response exceeds supported size")
	}
	if resp.StatusCode != http.StatusOK {
		return "", ModelUsage{}, fmt.Errorf("llm: status %d: %.300s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Usage *struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", ModelUsage{}, fmt.Errorf("llm: decode: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", ModelUsage{}, fmt.Errorf("llm: empty reply")
	}
	usage := ModelUsage{}
	if out.Usage != nil {
		usage.Input, usage.Output = out.Usage.Input, out.Usage.Output
	}
	return out.Choices[0].Message.Content, usage, nil
}
