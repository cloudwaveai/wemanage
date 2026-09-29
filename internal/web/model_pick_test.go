package web

import (
	"testing"
	"time"

	"aiseat/internal/store"
)

func TestPickDefaultModel(t *testing.T) {
	ids := func(created map[string]int64, names ...string) []store.ModelInfo {
		out := make([]store.ModelInfo, len(names))
		for i, n := range names {
			out[i] = store.ModelInfo{ID: n, Created: created[n]}
		}
		return out
	}
	// CPA v7.3.18's codex catalogue, with release times.
	created := map[string]int64{"gpt-5.5": 1776902400, "gpt-6-astra": 1783616400, "gpt-6-sol": 1790035200,
		"gpt-6-luna": 1790035200, "gpt-5.6-sol": 1783616400, "gpt-5.6-terra": 1783616400, "gpt-5.6-luna": 1783616400,
		"codex-auto-review": 1776902400}
	codex := ids(created, "gpt-5.5", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "codex-auto-review")
	noLuna6 := ids(created, "gpt-5.5", "gpt-6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "codex-auto-review")
	noLuna := ids(created, "gpt-5.5", "gpt-6-sol", "codex-auto-review")
	kimi := ids(nil, "kimi-k2.8-code", "kimi-k2", "kimi-k2.7-code", "kimi-k3", "kimi-k2.7-code-highspeed")

	for _, c := range []struct {
		name      string
		models    []store.ModelInfo
		chatgpt   bool
		preferred string
		want      string
	}{
		{"configured model available", codex, true, "gpt-6-luna", "gpt-6-luna"},
		{"same family fallback", noLuna6, true, "gpt-6-luna", "gpt-5.6-luna"},
		{"newest non-review", noLuna, true, "gpt-6-luna", "gpt-6-sol"},
		{"no list", nil, true, "gpt-6-luna", "gpt-6-luna"},
		{"kimi newest -code", kimi, false, "gpt-6-luna", "kimi-k2.8-code"},
		{"other provider without list", nil, false, "gpt-6-luna", ""},
		{"other provider without -code", ids(nil, "some-model"), false, "gpt-6-luna", "some-model"},
	} {
		if got := pickDefaultModel(c.models, c.chatgpt, c.preferred); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSeriesBucket(t *testing.T) {
	for _, c := range []struct {
		span time.Duration
		want time.Duration
	}{
		{30 * time.Minute, 5 * time.Minute},
		{3 * time.Hour, 15 * time.Minute},
		{20 * time.Hour, time.Hour},
		{6 * 24 * time.Hour, 24 * time.Hour},
	} {
		if got := seriesBucket(c.span); got != c.want {
			t.Errorf("seriesBucket(%v) = %v, want %v", c.span, got, c.want)
		}
	}
}
