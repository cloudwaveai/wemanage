package web_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The restore script takes the gateway out of config.toml and out of the CC
// Switch providers it leaked into, and leaves everything else alone.
func TestRestoreScriptGoesBackToOfficial(t *testing.T) {
	ts, _, _ := newServer(t)
	resp, err := ts.Client().Get(ts.URL + "/restore")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	script := string(raw)

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	polluted := `model_provider = "aiseat"
model = "gpt-6-luna"
model_reasoning_effort = "xhigh"

[projects."/Users/me"]
trust_level = "trusted"

[model_providers.aiseat]
name = "AI 订阅网关"
base_url = "https://ai.example.com/v1"
experimental_bearer_token = "sk-emp-secret"
http_headers = { "x-openai-actor-authorization" = "aiseat" }

[mcp_servers.x]
command = "x"
`
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(polluted), 0o600)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{"refresh_token":"r"}}`), 0o600)

	sqlite, haveSQLite := exec.LookPath("sqlite3")
	db := filepath.Join(home, ".cc-switch", "cc-switch.db")
	if haveSQLite == nil {
		_ = os.MkdirAll(filepath.Dir(db), 0o755)
		mk := exec.Command(sqlite, db)
		mk.Stdin = strings.NewReader(`CREATE TABLE providers (id TEXT, app_type TEXT, name TEXT, settings_config TEXT);
INSERT INTO providers VALUES ('codex-official','codex','OpenAI Official', json_object('auth', json_object('auth_mode','chatgpt'), 'config', '` + strings.ReplaceAll(polluted, "'", "''") + `'));
INSERT INTO providers VALUES ('gw','codex','AI 订阅网关', json_object('config', 'model_provider = "custom"' || char(10) || '[model_providers.custom]' || char(10) || 'base_url = "https://ai.example.com/v1"'));
INSERT INTO providers VALUES ('c','claude','x', json_object('config', '[model_providers.aiseat]'));`)
		if out, err := mk.CombinedOutput(); err != nil {
			t.Fatalf("make db: %v %s", err, out)
		}
	}

	run := exec.Command("sh", "-c", script)
	run.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	cfg, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	for _, gone := range []string{"aiseat", "sk-emp-secret", "ai.example.com"} {
		if strings.Contains(string(cfg), gone) {
			t.Errorf("config still has %q:\n%s", gone, cfg)
		}
	}
	for _, kept := range []string{`model = "gpt-6-luna"`, `model_reasoning_effort = "xhigh"`, `[projects."/Users/me"]`, "[mcp_servers.x]"} {
		if !strings.Contains(string(cfg), kept) {
			t.Errorf("config lost %q:\n%s", kept, cfg)
		}
	}
	if baks, _ := filepath.Glob(filepath.Join(codexDir, "config.toml.bak-*")); len(baks) != 1 {
		t.Errorf("backups: %v", baks)
	}
	if haveSQLite == nil {
		q := func(id string) string {
			o, _ := exec.Command(sqlite, db, "SELECT json_extract(settings_config,'$.config') FROM providers WHERE id='"+id+"'").Output()
			return string(o)
		}
		if off := q("codex-official"); strings.Contains(off, "aiseat") || !strings.Contains(off, "[mcp_servers.x]") {
			t.Errorf("CC Switch official not cleaned:\n%s\n%s", off, out)
		}
		if a, _ := exec.Command(sqlite, db, "SELECT json_extract(settings_config,'$.auth.auth_mode') FROM providers WHERE id='codex-official'").Output(); strings.TrimSpace(string(a)) != "chatgpt" {
			t.Errorf("official auth lost: %q", a)
		}
		if gw := q("gw"); !strings.Contains(gw, "ai.example.com") {
			t.Errorf("the gateway's own CC Switch provider must stay: %s", gw)
		}
		if c := q("c"); !strings.Contains(c, "aiseat") {
			t.Errorf("non-Codex providers are not touched: %s", c)
		}
	}

	t.Logf("restore output:\n%s", out)
	// A second run finds nothing to do.
	run = exec.Command("sh", "-c", script)
	run.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if out, err := run.CombinedOutput(); err != nil || !strings.Contains(string(out), "没有网关配置") {
		t.Fatalf("second run: %v\n%s", err, out)
	}
}
