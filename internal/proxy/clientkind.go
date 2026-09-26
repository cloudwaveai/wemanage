package proxy

import (
	"net/http"
	"regexp"

	"aiseat/internal/settings"
)

// KindCodex identifies clients that speak the Codex protocol/identity.
const KindCodex = "codex"

// KindOther is the default classification for everything else.
const KindOther = "other"

// compiledRule is a settings.ClientRule with its Pattern pre-compiled.
type compiledRule struct {
	kind   string
	header string
	re     *regexp.Regexp
}

// Classifier matches client headers to a kind using ordered rules; the first
// matching rule wins and its kind is returned, otherwise KindOther.
type Classifier struct {
	rules []compiledRule
}

// NewClassifier compiles each rule.Pattern, returning an error if any pattern
// is an invalid regular expression.
func NewClassifier(rules []settings.ClientRule) (*Classifier, error) {
	c := &Classifier{rules: make([]compiledRule, 0, len(rules))}
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, err
		}
		c.rules = append(c.rules, compiledRule{kind: r.Kind, header: r.Header, re: re})
	}
	return c, nil
}

// Classify returns the kind of the first rule whose header is present on h and
// whose pattern matches that header's value; if none match it returns KindOther.
func (c *Classifier) Classify(h http.Header) string {
	for _, r := range c.rules {
		v := h.Get(r.header)
		if v == "" {
			continue
		}
		if r.re.MatchString(v) {
			return r.kind
		}
	}
	return KindOther
}
