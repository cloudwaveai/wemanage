// Package workdigest writes the work board's daily summaries: what each
// employee worked on, and a team overview, from the day's conversation text.
// The LLM runs through one of the company's ChatGPT accounts via CPA.
package workdigest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"aiseat/internal/store"
)

// Limits on what one employee's day sends to the model. A busy day holds
// hundreds of thousands of characters, most of it context the client adds;
// what the person typed is a small part, and the start of each session says
// what it was about.
const (
	maxMessageRunes  = 400   // one message
	maxReplyRunes    = 400   // the closing assistant reply of a session
	maxMaterialRunes = 16000 // all of an employee's messages
	minSessionRunes  = 600   // a session's share never drops below this
	maxSessions      = 40
)

// noisePrefixes mark "user" turns the person did not type: context the Codex
// clients inject (environment, AGENTS.md, app context, multi-agent setup),
// compaction hand-offs and internal prompts of the desktop app.
var noisePrefixes = []string{
	"<", "[{", "You are ", "Another language model", "# AGENTS.md", "# Overview",
	"## Code review guidelines", "# Review Guidelines", "# Context from my IDE setup",
}

func isNoise(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// session is one conversation's material.
type session struct {
	first, last time.Time
	messages    []string
	reply       string // the last assistant text
}

// Material is one employee's day, ready for the model.
type Material struct {
	Sessions int
	Messages int
	Text     string
}

// BuildMaterial turns an employee's turns (ordered by time) into the text the
// model reads: per session, the messages the person typed (deduplicated,
// shortened) and the start of the last reply. Sessions share the budget, so a
// long one cannot crowd out the rest. Empty Text means nothing to summarize.
func BuildMaterial(turns []store.DayText, loc *time.Location) Material {
	if loc == nil {
		loc = time.UTC
	}
	byID := map[int64]*session{}
	var order []int64
	seen := map[string]bool{}
	for _, t := range turns {
		var id int64 // turns without a session share one
		if t.SessionID != nil {
			id = *t.SessionID
		}
		s := byID[id]
		if s == nil {
			s = &session{first: t.StartedAt}
			byID[id] = s
			order = append(order, id)
		}
		s.last = t.StartedAt
		text := strings.TrimSpace(t.Text)
		switch t.Role {
		case "user":
			if isNoise(text) || seen[text] {
				continue
			}
			seen[text] = true
			s.messages = append(s.messages, oneLine(truncate(text, maxMessageRunes)))
		case "assistant":
			if text != "" {
				s.reply = text
			}
		}
	}

	var sessions []*session
	messages := 0
	for _, id := range order {
		if s := byID[id]; len(s.messages) > 0 {
			sessions = append(sessions, s)
			messages += len(s.messages)
		}
	}
	if len(sessions) == 0 {
		return Material{}
	}
	if len(sessions) > maxSessions { // keep the busiest, in time order
		sort.SliceStable(sessions, func(i, j int) bool { return len(sessions[i].messages) > len(sessions[j].messages) })
		sessions = sessions[:maxSessions]
		sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].first.Before(sessions[j].first) })
	}

	share := max(maxMaterialRunes/len(sessions), minSessionRunes)
	var b strings.Builder
	for i, s := range sessions {
		fmt.Fprintf(&b, "## 会话 %d（%s–%s，%d 条消息）\n", i+1,
			s.first.In(loc).Format("15:04"), s.last.In(loc).Format("15:04"), len(s.messages))
		used := 0
		for j, m := range s.messages {
			n := len([]rune(m))
			if used > 0 && used+n > share {
				fmt.Fprintf(&b, "- ……（另有 %d 条消息未列出）\n", len(s.messages)-j)
				break
			}
			b.WriteString("- ")
			b.WriteString(m)
			b.WriteString("\n")
			used += n
		}
		if s.reply != "" {
			b.WriteString("助手最后的回复（开头）：")
			b.WriteString(oneLine(truncate(s.reply, maxReplyRunes)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return Material{Sessions: len(sessions), Messages: messages, Text: b.String()}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// oneLine folds whitespace runs (newlines included) into single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
