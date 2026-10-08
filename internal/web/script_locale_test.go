package web

import (
	"regexp"
	"testing"
)

// macOS's /bin/sh (bash 3.2) in a UTF-8 locale takes a multibyte character
// right after "$NAME" as part of the name, so "$TS）" fails under set -u
// ("TS?: unbound variable"). Every variable followed by non-ASCII text must
// be written ${NAME}, in every branch of the generated scripts.
func TestScriptsBraceVariablesBeforeNonASCII(t *testing.T) {
	bad := regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*[^\x00-\x7f]`)
	scripts := map[string]string{
		"install": shScript(installEntry{top: "TOP\n", table: "TABLE\n", key: "KEY", ccSettings: "{}"}, "ai.example.com", "https://ai.example.com"),
		"restore": restoreSh,
		"claude":  claudeCodeSh("https://ai.example.com"),
	}
	for name, s := range scripts {
		if m := bad.FindAllString(s, -1); len(m) > 0 {
			t.Errorf("%s script: unbraced variables before non-ASCII text: %q", name, m)
		}
	}
}
