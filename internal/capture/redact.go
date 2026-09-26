package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"aiseat/internal/settings"
)

// Redactor replaces secrets matching a set of named regexes with deterministic
// placeholders, so logged request/response bodies never retain live secrets
// while still allowing identical secrets to be recognized as identical.
type Redactor struct {
	rules []compiledRule
}

type compiledRule struct {
	name string
	re   *regexp.Regexp
}

// NewRedactor compiles the given rules. It returns an error if any pattern is
// not a valid regular expression.
func NewRedactor(rules []settings.RedactionRule) (*Redactor, error) {
	r := &Redactor{}
	for _, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("capture: redaction rule %q: %w", rule.Name, err)
		}
		r.rules = append(r.rules, compiledRule{name: rule.Name, re: re})
	}
	return r, nil
}

// Redact applies every rule in order, replacing each match with
// "[REDACTED:<name>:<first 8 hex chars of sha256(match)>]". The same secret
// always maps to the same placeholder. A nil or ruleless Redactor returns the
// input unchanged.
func (r *Redactor) Redact(b []byte) []byte {
	if r == nil || len(r.rules) == 0 {
		return b
	}
	out := b
	for _, rule := range r.rules {
		name := rule.name
		out = rule.re.ReplaceAllFunc(out, func(match []byte) []byte {
			sum := sha256.Sum256(match)
			return []byte(fmt.Sprintf("[REDACTED:%s:%s]", name, hex.EncodeToString(sum[:4])))
		})
	}
	return out
}

// DefaultRedactionRules returns the built-in secret patterns used when no
// custom rules are configured. It mirrors settings.DefaultRedactionRules so the
// two never drift apart.
func DefaultRedactionRules() []settings.RedactionRule {
	return settings.DefaultRedactionRules()
}
