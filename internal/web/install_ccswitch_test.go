package web_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// With CC Switch, the install command leaves config.toml to CC Switch: it
// cleans out an earlier direct install and adds the gateway as a CC Switch
// provider carrying the full config, so switching (and back to official)
// happens in CC Switch.
func TestInstallWithCCSwitchAddsAProvider(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	ts, _, _ := newServer(t)
	c := login(t, ts, "ou_cc")
	_, raw := c.do(http.MethodPost, "/api/me/key", "")
	var issued struct{ Key string }
	_ = json.Unmarshal(raw, &issued)

	script := func() string {
		_, raw := c.do(http.MethodPost, "/api/me/install-code", `{"key":"`+issued.Key+`"}`)
		var cmd struct{ Unix string }
		_ = json.Unmarshal(raw, &cmd)
		path := strings.TrimPrefix(strings.TrimSuffix(cmd.Unix, " | sh"), "curl -fsSL http://localhost")
		r, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := r.Body.Read(buf)
			b.Write(buf[:n])
			if e != nil {
				break
			}
		}
		return b.String()
	}

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	// Live config still carries an earlier direct install; "OpenAI Official"
	// in CC Switch already picked it up.
	live := "model_provider = \"aiseat\"\nmodel = \"gpt-6-luna\"\n\n[mcp_servers.x]\ncommand = \"x\"\n\n[model_providers.aiseat]\nexperimental_bearer_token = \"sk-emp-old\"\n"
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(live), 0o600)
	db := filepath.Join(home, ".cc-switch", "cc-switch.db")
	_ = os.MkdirAll(filepath.Dir(db), 0o755)
	mk := exec.Command(sqlite, db)
	mk.Stdin = strings.NewReader(`CREATE TABLE providers (id TEXT NOT NULL, app_type TEXT NOT NULL, name TEXT NOT NULL, settings_config TEXT NOT NULL,
  website_url TEXT, category TEXT, created_at INTEGER, sort_index INTEGER, notes TEXT, icon TEXT, icon_color TEXT,
  meta TEXT NOT NULL DEFAULT '{}', is_current BOOLEAN NOT NULL DEFAULT 0, in_failover_queue BOOLEAN NOT NULL DEFAULT 0,
  PRIMARY KEY (id, app_type));
INSERT INTO providers (id, app_type, name, settings_config, category, is_current) VALUES ('codex-official','codex','OpenAI Official',
  json_object('auth', json_object('auth_mode','chatgpt'), 'config', '` + strings.ReplaceAll(live, "'", "''") + `'), 'official', 1);
INSERT INTO providers (id, app_type, name, settings_config, website_url) VALUES ('old-import','codex','AI 订阅网关',
  json_object('config', 'model_provider = "custom"'), 'http://localhost');`)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("db: %v %s", err, out)
	}
	// A CC Switch running on this machine must not make the script wait.
	bin := filepath.Join(home, "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "pgrep"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	run := func() string {
		cmd := exec.Command("sh", "-c", script())
		cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH"), "SHELL=/bin/zsh"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		return string(out)
	}
	q := func(sql string) string {
		o, err := exec.Command(sqlite, db, sql).Output()
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return strings.TrimSpace(string(o))
	}

	out := run()
	cfg, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if strings.Contains(string(cfg), "aiseat") || !strings.Contains(string(cfg), "[mcp_servers.x]") {
		t.Fatalf("live config should be back to official, untouched otherwise:\n%s\n%s", cfg, out)
	}
	if off := q("SELECT json_extract(settings_config,'$.config') FROM providers WHERE id='codex-official'"); strings.Contains(off, "aiseat") {
		t.Fatalf("official still polluted:\n%s", off)
	}
	gw := q("SELECT json_extract(settings_config,'$.config') FROM providers WHERE id='aiseat-gateway'")
	for _, want := range []string{`model_provider = "custom"`, `experimental_bearer_token = "` + issued.Key + `"`,
		"requires_openai_auth = false", "x-openai-actor-authorization", "supports_standalone_web_search = true"} {
		if !strings.Contains(gw, want) {
			t.Errorf("CC Switch provider lacks %q:\n%s", want, gw)
		}
	}
	if q("SELECT name || '|' || is_current || '|' || json_extract(meta,'$.commonConfigEnabled') FROM providers WHERE id='aiseat-gateway'") != "公司 AI 网关|0|1" {
		t.Errorf("provider row: %s", q("SELECT * FROM providers WHERE id='aiseat-gateway'"))
	}
	if !strings.Contains(out, "「AI 订阅网关」是以前导入的网关") || !strings.Contains(out, "点「启用」") {
		t.Errorf("output:\n%s", out)
	}

	// The gateway in use and a new key: the row is updated, not duplicated,
	// and the live config gets the key too.
	_ = exec.Command(sqlite, db, "UPDATE providers SET is_current = (id='aiseat-gateway')").Run()
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model_provider = \"custom\"\n\n[model_providers.custom]\nexperimental_bearer_token = \"sk-emp-stale\"\n"), 0o600)
	_, raw = c.do(http.MethodPost, "/api/me/key", "")
	_ = json.Unmarshal(raw, &issued)
	out = run()
	if n := q("SELECT count(*) FROM providers WHERE id='aiseat-gateway'"); n != "1" {
		t.Fatalf("rows: %s", n)
	}
	if !strings.Contains(q("SELECT json_extract(settings_config,'$.config') FROM providers WHERE id='aiseat-gateway'"), issued.Key) {
		t.Fatal("the new key is not in CC Switch")
	}
	cfg, _ = os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if !strings.Contains(string(cfg), `experimental_bearer_token = "`+issued.Key+`"`) || strings.Contains(string(cfg), "sk-emp-stale") {
		t.Fatalf("live config of the provider in use:\n%s\n%s", cfg, out)
	}
}
