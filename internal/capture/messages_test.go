package capture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const claudeStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"gpt-6-luna","usage":{"input_tokens":120,"cache_read_input_tokens":900,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"看一下"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"文件。"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"Read","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

func TestMessagesStream(t *testing.T) {
	p := NewStreamParser("messages", time.Now())
	// Odd chunk boundaries, like a real network read.
	for _, part := range strings.SplitAfter(claudeStream, "a") {
		_, _ = p.Write([]byte(part))
	}
	o := p.Finish()
	if o.Model != "gpt-6-luna" || !o.Completed || !o.HasTTFT || o.ErrorType != "" {
		t.Fatalf("outcome: %+v", o)
	}
	if o.Usage != (Usage{Input: 1020, Cached: 900, Output: 42}) {
		t.Fatalf("usage: %+v", o.Usage)
	}
	if len(o.OutputItems) != 1 {
		t.Fatalf("items: %d", len(o.OutputItems))
	}
	var msg struct {
		Role    string
		Content []map[string]any
	}
	_ = json.Unmarshal(o.OutputItems[0], &msg)
	if msg.Role != "assistant" || len(msg.Content) != 2 || msg.Content[0]["text"] != "看一下文件。" ||
		msg.Content[1]["name"] != "Read" || msg.Content[1]["input"].(map[string]any)["file_path"] != "a.go" {
		t.Fatalf("message: %s", o.OutputItems[0])
	}
	if got := extractOutputText("messages", o.OutputItems[0]); got != "看一下文件。" {
		t.Fatalf("text: %q", got)
	}
}

func TestMessagesJSONAndRequest(t *testing.T) {
	o := ParseJSONResponse("messages", []byte(`{"model":"kimi-k2.8","content":[{"type":"text","text":"好"}],"usage":{"input_tokens":5,"output_tokens":2}}`))
	if o.Model != "kimi-k2.8" || o.Usage.Input != 5 || o.Usage.Output != 2 || len(o.OutputItems) != 1 {
		t.Fatalf("json: %+v", o)
	}
	body := []byte(`{"model":"gpt-6-luna","system":[{"type":"text","text":"You are Claude Code"}],"tools":[{"name":"Read"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"修一下这个 bug"}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file body"}]}]}`)
	red, _ := NewRedactor(nil)
	segs, inputStart, err := SplitRequest("messages", body, red)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, s := range segs {
		kinds = append(kinds, s.Kind)
	}
	if strings.Join(kinds, ",") != "params,instructions,tools,input,input" || inputStart != 3 {
		t.Fatalf("segments: %v start %d", kinds, inputStart)
	}
	if strings.Contains(string(segs[0].Data), "Claude Code") {
		t.Fatalf("system prompt left in params: %s", segs[0].Data)
	}
	if got := extractInputText("messages", segs[3].Data); got != "修一下这个 bug" {
		t.Fatalf("input text: %q", got)
	}
	if got := extractInputText("messages", segs[4].Data); got != "" {
		t.Fatalf("a tool result is not the person's text: %q", got)
	}
}
