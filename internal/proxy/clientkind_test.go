package proxy

import (
	"net/http"
	"testing"

	"aiseat/internal/settings"
)

func defaultClientRules() []settings.ClientRule {
	return []settings.ClientRule{
		{Kind: "codex", Header: "User-Agent", Pattern: `^(codex[-_ ]|codex_cli_rs|codex-tui|codex_vscode|codex_exec)`},
		{Kind: "codex", Header: "Originator", Pattern: `^codex`},
	}
}

func TestNewClassifierInvalidPattern(t *testing.T) {
	if _, err := NewClassifier([]settings.ClientRule{
		{Kind: "codex", Header: "User-Agent", Pattern: `^(codex`},
	}); err == nil {
		t.Fatal("expected error for invalid regex pattern, got nil")
	}
}

func TestClassifyDefaultRules(t *testing.T) {
	c, err := NewClassifier(defaultClientRules())
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}

	// codex_cli_rs User-Agent matches the first rule.
	h := http.Header{}
	h.Set("User-Agent", "codex_cli_rs/0.157.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11")
	if got := c.Classify(h); got != KindCodex {
		t.Errorf("codex_cli_rs UA: got %q, want %q", got, KindCodex)
	}

	// Non-codex UA but Originator matches the second rule.
	h = http.Header{}
	h.Set("User-Agent", "OpenAI/JS 4.0")
	h.Set("Originator", "codex_vscode")
	if got := c.Classify(h); got != KindCodex {
		t.Errorf("codex_vscode originator: got %q, want %q", got, KindCodex)
	}

	// Neither header matches -> other.
	h = http.Header{}
	h.Set("User-Agent", "OpenAI/JS 4.0")
	if got := c.Classify(h); got != KindOther {
		t.Errorf("OpenAI UA no originator: got %q, want %q", got, KindOther)
	}

	// Empty header set -> other.
	if got := c.Classify(http.Header{}); got != KindOther {
		t.Errorf("empty header: got %q, want %q", got, KindOther)
	}
}

func TestClassifyFirstMatchWins(t *testing.T) {
	c, err := NewClassifier([]settings.ClientRule{
		{Kind: "other", Header: "User-Agent", Pattern: `^OpenAI`},
		{Kind: "codex", Header: "Originator", Pattern: `^codex`},
	})
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}
	h := http.Header{}
	h.Set("User-Agent", "OpenAI/JS 4.0")
	h.Set("Originator", "codex")
	if got := c.Classify(h); got != "other" {
		t.Errorf("first match wins: got %q, want %q", got, "other")
	}
}
