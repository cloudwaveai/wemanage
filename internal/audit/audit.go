// Package audit checks the newest user message of a request against the
// company's content rules before it goes upstream. OpenAI bans accounts for
// cyber abuse (malware, credential theft, AV evasion, ...); with one account
// per employee a single bad prompt costs that employee's paid seat.
package audit

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"aiseat/internal/settings"

	"github.com/tidwall/gjson"
)

// maxScan bounds how much of the newest message is scanned (its tail).
const maxScan = 32 << 10

// Hit is a matched rule and a short excerpt around the match.
type Hit struct {
	Rule    string
	Snippet string
}

type compiled struct {
	name string
	re   *regexp.Regexp
}

// Auditor caches compiled rules between requests.
type Auditor struct {
	mu    sync.Mutex
	rules []settings.ContentRule
	comp  []compiled
}

func (a *Auditor) compiledFor(rules []settings.ContentRule) []compiled {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.comp != nil && sameRules(a.rules, rules) {
		return a.comp
	}
	comp := make([]compiled, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile("(?i)" + r.Pattern)
		if err != nil {
			continue // settings validation rejects these; skip defensively
		}
		comp = append(comp, compiled{name: r.Name, re: re})
	}
	a.rules = append([]settings.ContentRule(nil), rules...)
	a.comp = comp
	return comp
}

func sameRules(a, b []settings.ContentRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Check returns the first rule matching the newest user message, if auditing
// is on.
func (a *Auditor) Check(set settings.Settings, endpoint string, body []byte) (Hit, bool) {
	if set.ContentAuditMode == settings.AuditOff || set.ContentAuditMode == "" || len(set.ContentAuditRules) == 0 {
		return Hit{}, false
	}
	text := LatestUserText(endpoint, body)
	if text == "" {
		return Hit{}, false
	}
	for _, c := range a.compiledFor(set.ContentAuditRules) {
		if loc := c.re.FindStringIndex(text); loc != nil {
			return Hit{Rule: c.name, Snippet: excerpt(text, loc[0], loc[1])}, true
		}
	}
	return Hit{}, false
}

// LatestUserText extracts the text of the newest user message: the last
// role=user item of a Responses API `input` (or the input string itself), or
// the last user message of a Chat Completions or Anthropic Messages
// `messages` array. Tool outputs
// and earlier turns are ignored, so a flagged turn is reported once.
func LatestUserText(endpoint string, body []byte) string {
	var items gjson.Result
	if endpoint == "chat" || endpoint == "messages" {
		items = gjson.GetBytes(body, "messages")
	} else {
		items = gjson.GetBytes(body, "input")
		if items.Type == gjson.String {
			return tail(items.String())
		}
	}
	if !items.IsArray() {
		return ""
	}
	arr := items.Array()
	for i := len(arr) - 1; i >= 0; i-- {
		it := arr[i]
		if it.Get("role").String() != "user" {
			continue
		}
		c := it.Get("content")
		if c.Type == gjson.String {
			return tail(c.String())
		}
		var b strings.Builder
		c.ForEach(func(_, part gjson.Result) bool {
			if t := part.Get("text"); t.Exists() {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t.String())
			}
			return true
		})
		return tail(b.String())
	}
	return ""
}

func tail(s string) string {
	if len(s) <= maxScan {
		return s
	}
	s = s[len(s)-maxScan:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// excerpt returns up to ~40 characters on each side of the match, on one line.
func excerpt(s string, start, end int) string {
	from, to := start, end
	for n := 0; n < 40 && from > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(s[:from])
		from -= size
	}
	for n := 0; n < 40 && to < len(s); n++ {
		_, size := utf8.DecodeRuneInString(s[to:])
		to += size
	}
	out := strings.Join(strings.Fields(s[from:to]), " ")
	if from > 0 {
		out = "…" + out
	}
	if to < len(s) {
		out += "…"
	}
	return out
}
