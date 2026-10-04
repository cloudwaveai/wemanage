package capture

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// TextRow is a human-readable turn extracted from a captured exchange.
type TextRow struct {
	Role string // "user" or "assistant"
	Text string
}

// maxTextChars bounds each extracted text (rune-safe truncation).
const maxTextChars = 8000

// ExtractTexts returns the session-new user inputs plus the assistant outputs
// of one exchange.
//
// reqSegs and inputStart come from SplitRequest. prevInputCount is the number
// of input elements in the previous request of the session (0 if none): only
// input segments at index >= inputStart+prevInputCount are session-new and
// contribute user text. Assistant text comes from out.OutputItems. Every text
// is truncated to maxTextChars runes and empties are skipped.
func ExtractTexts(endpoint string, reqSegs []Segment, inputStart, prevInputCount int, out Outcome) []TextRow {
	var rows []TextRow

	from := inputStart + prevInputCount
	if from < 0 {
		from = 0
	}
	for i := from; i < len(reqSegs); i++ {
		if reqSegs[i].Kind != "input" {
			continue
		}
		if text := extractInputText(endpoint, reqSegs[i].Data); text != "" {
			rows = append(rows, TextRow{Role: "user", Text: truncateRunes(text, maxTextChars)})
		}
	}

	for _, item := range out.OutputItems {
		if text := extractOutputText(endpoint, item); text != "" {
			rows = append(rows, TextRow{Role: "assistant", Text: truncateRunes(text, maxTextChars)})
		}
	}

	return rows
}

// extractInputText pulls human-readable text from a single request input
// element.
func extractInputText(endpoint string, data []byte) string {
	if endpoint == "chat" || endpoint == "messages" {
		return chatMessageText(gjson.ParseBytes(data))
	}
	return responsesInputText(gjson.ParseBytes(data))
}

// extractOutputText pulls human-readable assistant text from a single output
// item.
func extractOutputText(endpoint string, item json.RawMessage) string {
	root := gjson.ParseBytes(item)
	if endpoint == "chat" || endpoint == "messages" {
		return chatMessageText(root)
	}
	// responses: gather output_text parts, else a message's top-level text.
	if c := root.Get("content"); c.IsArray() {
		if s := joinTypedParts(c, "output_text"); s != "" {
			return s
		}
	}
	if t := root.Get("text"); t.Type == gjson.String && t.String() != "" {
		return t.String()
	}
	return ""
}

// responsesInputText applies the responses input-element rules: content[].text
// of input_text/output_text parts, else a top-level text field, else a string
// content alongside a role.
func responsesInputText(root gjson.Result) string {
	if c := root.Get("content"); c.IsArray() {
		if s := joinTypedParts(c, "input_text", "output_text"); s != "" {
			return s
		}
	}
	if t := root.Get("text"); t.Type == gjson.String && t.String() != "" {
		return t.String()
	}
	if root.Get("role").Exists() {
		if c := root.Get("content"); c.Type == gjson.String {
			return c.String()
		}
	}
	return ""
}

// chatMessageText applies the chat message rules: a string content, else the
// concatenation of content[].text parts.
func chatMessageText(root gjson.Result) string {
	c := root.Get("content")
	if c.Type == gjson.String {
		return c.String()
	}
	if c.IsArray() {
		return joinAllParts(c)
	}
	return ""
}

// joinTypedParts concatenates the text of array parts whose "type" is one of
// the given types.
func joinTypedParts(arr gjson.Result, types ...string) string {
	var sb strings.Builder
	arr.ForEach(func(_, v gjson.Result) bool {
		pt := v.Get("type").String()
		for _, want := range types {
			if pt == want {
				appendPiece(&sb, v.Get("text").String())
				break
			}
		}
		return true
	})
	return sb.String()
}

// joinAllParts concatenates the "text" of every array part that has one.
func joinAllParts(arr gjson.Result) string {
	var sb strings.Builder
	arr.ForEach(func(_, v gjson.Result) bool {
		appendPiece(&sb, v.Get("text").String())
		return true
	})
	return sb.String()
}

func appendPiece(sb *strings.Builder, s string) {
	if s == "" {
		return
	}
	sb.WriteString(s)
}

// truncateRunes returns s limited to at most max runes, never splitting a rune.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}
