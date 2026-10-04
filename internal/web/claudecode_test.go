package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The claude-gw alias starts Claude Code with the gateway's settings and
// leaves the shell's own environment alone.
func TestClaudeCodeShellFunction(t *testing.T) {
	dir := t.TempDir()
	fake := "#!/bin/sh\necho \"BASE=$ANTHROPIC_BASE_URL TOKEN=$ANTHROPIC_AUTH_TOKEN MODEL=$ANTHROPIC_MODEL HAIKU=$ANTHROPIC_DEFAULT_HAIKU_MODEL ARGS=$*\"\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	fn := strings.NewReplacer("{{KEY}}", "sk-emp-abc", "{{MODEL}}", "gpt-6-luna").Replace(claudeCodeSh("https://ai.example.com"))
	for _, sh := range []string{"sh", "bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		script := filepath.Join(dir, "run.sh")
		_ = os.WriteFile(script, []byte(fn+"\nclaude-gw -p hi\necho \"AFTER=${ANTHROPIC_BASE_URL:-unset}\"\n"), 0o644)
		args := []string{script}
		if sh != "zsh" {
			args = []string{"-c", "shopt -s expand_aliases 2>/dev/null; . " + script}
		}
		cmd := exec.Command(sh, args...)
		cmd.Env = []string{"PATH=" + dir + ":" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", sh, err, out)
		}
		want := "BASE=https://ai.example.com TOKEN=sk-emp-abc MODEL=gpt-6-luna HAIKU=gpt-6-luna ARGS=-p hi\nAFTER=unset\n"
		if string(out) != want {
			t.Fatalf("%s:\n%s\nwant\n%s", sh, out, want)
		}
	}
}
