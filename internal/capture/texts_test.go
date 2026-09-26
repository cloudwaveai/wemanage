package capture

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func mustSplit(t *testing.T, endpoint string, body []byte) ([]Segment, int) {
	t.Helper()
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	segs, inputStart, err := SplitRequest(endpoint, body, r)
	if err != nil {
		t.Fatalf("SplitRequest: %v", err)
	}
	return segs, inputStart
}

func TestExtractTextsResponsesSessionNewAndTruncation(t *testing.T) {
	long := strings.Repeat("世", 9000) // 9000 runes, multibyte
	body := []byte(fmt.Sprintf(`{
		"model":"m",
		"instructions":"sys",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"user","content":[{"type":"input_text","text":%q}]}
		]
	}`, long))

	segs, inputStart := mustSplit(t, "responses", body)
	if inputStart != 2 { // params at 0, instructions at 1, inputs at 2,3
		t.Fatalf("inputStart = %d, want 2", inputStart)
	}

	out := Outcome{OutputItems: []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer"}]}`),
	}}

	// prevInputCount=1 => only the 2nd input (index inputStart+1) is new.
	rows := ExtractTexts("responses", segs, inputStart, 1, out)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Role != "user" {
		t.Fatalf("row0 role = %q, want user", rows[0].Role)
	}
	if strings.Contains(rows[0].Text, "hi") {
		t.Fatalf("first (already-seen) input leaked into rows")
	}
	if n := utf8.RuneCountInString(rows[0].Text); n != 8000 {
		t.Fatalf("truncated rune count = %d, want 8000", n)
	}
	if !utf8.ValidString(rows[0].Text) {
		t.Fatalf("truncation split a rune (invalid UTF-8)")
	}
	if rows[1].Role != "assistant" || rows[1].Text != "the answer" {
		t.Fatalf("assistant row = %+v, want {assistant, the answer}", rows[1])
	}
}

func TestExtractTextsResponsesAllWhenNoPrev(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"role":"user","content":[{"type":"input_text","text":"one"}]},
		{"role":"user","content":[{"type":"input_text","text":"two"}]}
	]}`)
	segs, inputStart := mustSplit(t, "responses", body)

	rows := ExtractTexts("responses", segs, inputStart, 0, Outcome{})
	if len(rows) != 2 {
		t.Fatalf("want 2 user rows, got %d", len(rows))
	}
	if rows[0].Text != "one" || rows[1].Text != "two" {
		t.Fatalf("rows = %+v, want [one two]", rows)
	}
}

func TestExtractTextsChat(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":"first"},
		{"role":"user","content":"second"}
	]}`)
	segs, inputStart := mustSplit(t, "chat", body)

	out := Outcome{OutputItems: []json.RawMessage{
		json.RawMessage(`{"role":"assistant","content":"reply"}`),
	}}

	// prevInputCount=1 => only "second" contributes on the user side.
	rows := ExtractTexts("chat", segs, inputStart, 1, out)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Role != "user" || rows[0].Text != "second" {
		t.Fatalf("user row = %+v, want {user, second}", rows[0])
	}
	if rows[1].Role != "assistant" || rows[1].Text != "reply" {
		t.Fatalf("assistant row = %+v, want {assistant, reply}", rows[1])
	}
}

func TestExtractTextsChatArrayContent(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
		{"role":"user","content":[{"type":"text","text":"ab"},{"type":"text","text":"cd"}]}
	]}`)
	segs, inputStart := mustSplit(t, "chat", body)

	rows := ExtractTexts("chat", segs, inputStart, 0, Outcome{})
	if len(rows) != 1 || rows[0].Text != "abcd" {
		t.Fatalf("array content rows = %+v, want [abcd]", rows)
	}
}
