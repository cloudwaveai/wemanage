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

func TestInstallCommandWritesCodexConfig(t *testing.T) {
	ts, _, _ := newServer(t)
	c := login(t, ts, "ou_inst")
	_, raw := c.do(http.MethodPost, "/api/me/key", "")
	var issued struct{ Key string }
	_ = json.Unmarshal(raw, &issued)
	if issued.Key == "" {
		t.Fatalf("no key: %s", raw)
	}

	if resp, _ := c.do(http.MethodPost, "/api/me/install-code", `{"key":"sk-emp-not-mine-at-all-1234"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("someone else's key: status %d", resp.StatusCode)
	}
	resp, raw := c.do(http.MethodPost, "/api/me/install-code", `{"key":"`+issued.Key+`"}`)
	var cmd struct{ Unix, Windows string }
	_ = json.Unmarshal(raw, &cmd)
	if resp.StatusCode != 200 || !strings.HasPrefix(cmd.Unix, "curl -fsSL http://localhost/i/") || !strings.HasSuffix(cmd.Windows, ".ps1 | iex") {
		t.Fatalf("install-code: %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(cmd.Unix, issued.Key) {
		t.Fatal("the key must not appear in the command")
	}
	path := strings.TrimPrefix(strings.TrimSuffix(cmd.Unix, " | sh"), "curl -fsSL http://localhost")

	get := func() string {
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
	script := get()
	if !strings.Contains(script, issued.Key) {
		t.Fatalf("script lacks the key:\n%s", script)
	}
	if again := get(); strings.Contains(again, issued.Key) || !strings.Contains(again, "已经失效") {
		t.Fatalf("a code must work once:\n%s", again)
	}

	// Run it against an existing config: our settings replace the old model
	// lines and aiseat table; everything else stays.
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	old := `model = "o3"
model_provider = "openai"
approval_policy = "on-request"

[model_providers.aiseat]
name = "old"
experimental_bearer_token = "sk-emp-old"

[mcp_servers.docs]
command = "docs-mcp"
model = "kept-inside-a-table"
`
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("sh", "-c", script)
	run.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SHELL=/bin/zsh", "HTTPS_PROXY=http://127.0.0.1:7890"}
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	cfgBytes, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	cfg := string(cfgBytes)
	for _, want := range []string{
		"model_provider = \"aiseat\"\nmodel = \"gpt-6-luna\"\n",
		`approval_policy = "on-request"`,
		"[mcp_servers.docs]\ncommand = \"docs-mcp\"\nmodel = \"kept-inside-a-table\"",
		`experimental_bearer_token = "` + issued.Key + `"`,
		`x-openai-actor-authorization`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	for _, gone := range []string{`model = "o3"`, `model_provider = "openai"`, `sk-emp-old`, `name = "old"`} {
		if strings.Contains(cfg, gone) {
			t.Errorf("config still has %q:\n%s", gone, cfg)
		}
	}
	if n := strings.Count(cfg, "[model_providers.aiseat]"); n != 1 {
		t.Errorf("aiseat tables = %d:\n%s", n, cfg)
	}
	// Valid TOML (checked with Python's tomllib where available).
	if py, err := exec.LookPath("python3"); err == nil {
		check := exec.Command(py, "-c", "import sys, tomllib; tomllib.load(open(sys.argv[1], 'rb'))", filepath.Join(codexDir, "config.toml"))
		if out, err := check.CombinedOutput(); err != nil && !strings.Contains(string(out), "No module named 'tomllib'") {
			t.Errorf("config is not valid TOML: %s\n%s", out, cfg)
		}
	}
	if st, _ := os.Stat(filepath.Join(codexDir, "config.toml")); st.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 0600 (it holds the key)", st.Mode().Perm())
	}
	baks, _ := filepath.Glob(filepath.Join(codexDir, "config.toml.bak-*"))
	if len(baks) != 1 {
		t.Errorf("backups: %v", baks)
	}
	rc, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if !strings.Contains(string(rc), `NO_PROXY="${NO_PROXY:+$NO_PROXY,}localhost"`) {
		t.Errorf("zshrc lacks NO_PROXY:\n%s\nscript output:\n%s", rc, out)
	}
	// Running again (a fresh code) does not add a second NO_PROXY block.
	_, raw = c.do(http.MethodPost, "/api/me/install-code", `{"key":"`+issued.Key+`"}`)
	_ = json.Unmarshal(raw, &cmd)
	path = strings.TrimPrefix(strings.TrimSuffix(cmd.Unix, " | sh"), "curl -fsSL http://localhost")
	run = exec.Command("sh", "-c", get())
	run.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "SHELL=/bin/zsh", "HTTPS_PROXY=http://127.0.0.1:7890"}
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	rc, _ = os.ReadFile(filepath.Join(home, ".zshrc"))
	if n := strings.Count(string(rc), "AI 订阅网关直连"); n != 1 {
		t.Errorf("NO_PROXY blocks = %d", n)
	}
	cfgBytes, _ = os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if n := strings.Count(string(cfgBytes), "[model_providers.aiseat]"); n != 1 {
		t.Errorf("after a second run, aiseat tables = %d:\n%s", n, cfgBytes)
	}
}
