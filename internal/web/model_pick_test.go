package web

import "testing"

func TestPickDefaultModel(t *testing.T) {
	kimi := []string{"kimi-k2.8-code", "kimi-k2", "kimi-k2.6", "kimi-k2.7-code", "kimi-k2.8", "kimi-k3-256k",
		"kimi-k2-thinking", "kimi-k2.7-code-highspeed", "kimi-k2.5", "kimi-k3"}
	codex := []string{"gpt-5", "gpt-5-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1"}
	for _, c := range []struct {
		ids     []string
		chatgpt bool
		want    string
	}{
		{kimi, false, "kimi-k2.8-code"},
		{codex, true, "gpt-5.1-codex"},
		{[]string{"some-model"}, false, "some-model"},
		{nil, true, "gpt-5.1-codex"},
		{nil, false, ""},
	} {
		if got := pickDefaultModel(c.ids, c.chatgpt); got != c.want {
			t.Errorf("pickDefaultModel(%v) = %q, want %q", c.ids, got, c.want)
		}
	}
}
