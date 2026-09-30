package web

import "testing"

func TestCodexClient(t *testing.T) {
	cases := []struct {
		ua, client, version string
		cli                 bool
	}{
		{"codex_cli_rs/0.159.0 (Mac OS 15.6.0; arm64) iTerm.app/3.5.10", "Codex 命令行", "0.159.0", true},
		{"codex-tui/0.157.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.157.1)", "Codex 命令行", "0.157.1", true},
		{"codex_vscode/0.158.0 (Windows 11; x86_64)", "Codex 编辑器插件", "0.158.0", true},
		{"Codex Desktop/1.3.0 (Mac OS 15.6.0; arm64)", "Codex 桌面版", "1.3.0", false},
		{"curl/8.7.1", "curl", "8.7.1", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		client, version, cli := codexClient(c.ua)
		if client != c.client || version != c.version || cli != c.cli {
			t.Errorf("codexClient(%q) = %q, %q, %v; want %q, %q, %v", c.ua, client, version, cli, c.client, c.version, c.cli)
		}
	}
}
