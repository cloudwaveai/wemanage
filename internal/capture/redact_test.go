package capture

import (
	"strings"
	"testing"

	"aiseat/internal/settings"
)

func TestRedactorReplacesSecrets(t *testing.T) {
	r, err := NewRedactor(DefaultRedactionRules())
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}

	in := []byte("my key is sk-ABCDEFGHIJKLMNOPQRSTUV and cert:\n-----BEGIN RSA PRIVATE KEY-----\nMIIB...\n-----END RSA PRIVATE KEY-----")
	out := string(r.Redact(in))

	if strings.Contains(out, "sk-ABCDEFGHIJKLMNOPQRSTUV") {
		t.Fatalf("openai key not redacted: %s", out)
	}
	if !strings.Contains(out, "[REDACTED:openai-key:") {
		t.Fatalf("missing openai-key placeholder: %s", out)
	}
	if strings.Contains(out, "-----BEGIN RSA PRIVATE KEY-----") {
		t.Fatalf("private key header not redacted: %s", out)
	}
	if !strings.Contains(out, "[REDACTED:private-key:") {
		t.Fatalf("missing private-key placeholder: %s", out)
	}
}

func TestRedactorLeavesPlainTextAlone(t *testing.T) {
	r, err := NewRedactor(DefaultRedactionRules())
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	in := []byte("hello world")
	if got := string(r.Redact(in)); got != "hello world" {
		t.Fatalf("plain text changed: %q", got)
	}
}

func TestRedactorDeterministic(t *testing.T) {
	r, err := NewRedactor(DefaultRedactionRules())
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	in := []byte("first sk-ABCDEFGHIJKLMNOPQRSTUV then sk-ABCDEFGHIJKLMNOPQRSTUV")
	out := string(r.Redact(in))

	// Same secret twice -> same placeholder, and it must appear exactly twice.
	if n := strings.Count(out, "[REDACTED:openai-key:"); n != 2 {
		t.Fatalf("expected 2 placeholders, got %d: %s", n, out)
	}
	fields := strings.Fields(out)
	if len(fields) != 4 {
		t.Fatalf("unexpected token layout: %q", out)
	}
	if fields[1] != fields[3] {
		t.Fatalf("same secret produced different placeholders: %q vs %q", fields[1], fields[3])
	}
}

func TestRedactorNilAndEmptyRules(t *testing.T) {
	// nil Redactor returns input unchanged.
	var nilR *Redactor
	in := []byte("sk-ABCDEFGHIJKLMNOPQRSTUV")
	if got := string(nilR.Redact(in)); got != string(in) {
		t.Fatalf("nil redactor mutated input: %q", got)
	}
	// Empty rule set returns input unchanged.
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	if got := string(r.Redact(in)); got != string(in) {
		t.Fatalf("empty redactor mutated input: %q", got)
	}
}

func TestNewRedactorBadRegex(t *testing.T) {
	_, err := NewRedactor([]settings.RedactionRule{{Name: "bad", Pattern: "([unterminated"}})
	if err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

func TestDefaultRedactionRules(t *testing.T) {
	if got := len(DefaultRedactionRules()); got != 7 {
		t.Fatalf("expected 7 default rules, got %d", got)
	}
}
