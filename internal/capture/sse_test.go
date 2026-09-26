package capture

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// feedChunks writes data to a fresh parser in fixed-size chunks (0 => whole).
func feedChunks(endpoint string, data []byte, size int) Outcome {
	p := NewStreamParser(endpoint, time.Now())
	if size <= 0 {
		_, _ = p.Write(data)
	} else {
		for i := 0; i < len(data); i += size {
			end := i + size
			if end > len(data) {
				end = len(data)
			}
			_, _ = p.Write(data[i:end])
		}
	}
	return p.Finish()
}

func TestResponsesStream(t *testing.T) {
	data := readFixture(t, "responses_stream.sse")
	oc := feedChunks("responses", data, 0)

	if oc.Model != "gpt-4o-2024-08-06" {
		t.Errorf("Model = %q, want gpt-4o-2024-08-06", oc.Model)
	}
	if oc.Usage.Input <= 0 || oc.Usage.Cached <= 0 || oc.Usage.Output <= 0 || oc.Usage.Reasoning <= 0 {
		t.Errorf("Usage has non-positive field: %+v", oc.Usage)
	}
	if oc.Usage != (Usage{Input: 36, Cached: 12, Output: 24, Reasoning: 8}) {
		t.Errorf("Usage = %+v, want {36 12 24 8}", oc.Usage)
	}
	if len(oc.OutputItems) < 1 {
		t.Errorf("len(OutputItems) = %d, want >= 1", len(oc.OutputItems))
	}
	if !oc.HasTTFT {
		t.Error("HasTTFT = false, want true")
	}
	if oc.TTFT < 0 {
		t.Errorf("TTFT = %v, want >= 0", oc.TTFT)
	}
	if !oc.Completed {
		t.Error("Completed = false, want true")
	}
}

func TestChatStream(t *testing.T) {
	data := readFixture(t, "chat_stream.sse")
	oc := feedChunks("chat", data, 0)

	if !oc.Completed {
		t.Error("Completed = false, want true (saw [DONE])")
	}
	if oc.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini", oc.Model)
	}
	if oc.Usage != (Usage{Input: 50, Cached: 10, Output: 18, Reasoning: 4}) {
		t.Errorf("Usage = %+v, want {50 10 18 4}", oc.Usage)
	}
	if len(oc.OutputItems) != 1 {
		t.Fatalf("len(OutputItems) = %d, want 1", len(oc.OutputItems))
	}

	var msg struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(oc.OutputItems[0], &msg); err != nil {
		t.Fatalf("unmarshal assembled message: %v (%s)", err, oc.OutputItems[0])
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if msg.Content != "Hello, world" {
		t.Errorf("content = %q, want %q", msg.Content, "Hello, world")
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(tool_calls) = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("tool_call id = %q, want call_abc", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("tool_call type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("tool_call name = %q, want get_weather", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"location":"Paris"}` {
		t.Errorf("tool_call arguments = %q, want %q", tc.Function.Arguments, `{"location":"Paris"}`)
	}
	if !oc.HasTTFT {
		t.Error("HasTTFT = false, want true")
	}
}

func TestChunkBoundaryInvariance(t *testing.T) {
	data := readFixture(t, "responses_stream.sse")
	whole := feedChunks("responses", data, 0)
	for _, size := range []int{1, 7} {
		got := feedChunks("responses", data, size)
		if got.Model != whole.Model {
			t.Errorf("size=%d Model = %q, want %q", size, got.Model, whole.Model)
		}
		if got.Usage != whole.Usage {
			t.Errorf("size=%d Usage = %+v, want %+v", size, got.Usage, whole.Usage)
		}
		if len(got.OutputItems) != len(whole.OutputItems) {
			t.Errorf("size=%d len(OutputItems) = %d, want %d", size, len(got.OutputItems), len(whole.OutputItems))
		}
	}
}

func TestTruncatedStream(t *testing.T) {
	data := readFixture(t, "responses_stream.sse")
	idx := bytes.Index(data, []byte("event: response.completed"))
	if idx < 0 {
		t.Fatal("fixture missing response.completed event")
	}
	truncated := data[:idx] // drop the terminal completed event entirely

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parser panicked on truncated stream: %v", r)
		}
	}()

	// Feed at several chunk sizes to exercise mid-line truncation handling.
	for _, size := range []int{0, 1, 7} {
		oc := feedChunks("responses", truncated, size)
		if oc.Completed {
			t.Errorf("size=%d Completed = true, want false for truncated stream", size)
		}
	}
}

func TestResponsesFailed(t *testing.T) {
	data := readFixture(t, "responses_failed.sse")
	oc := feedChunks("responses", data, 0)

	if oc.ErrorType != "server_error" {
		t.Errorf("ErrorType = %q, want server_error", oc.ErrorType)
	}
	if !oc.Completed {
		t.Error("Completed = false, want true (response.failed terminates the stream)")
	}
}

func TestParseJSONResponses(t *testing.T) {
	oc := ParseJSONResponse("responses", readFixture(t, "responses.json"))
	if oc.Model != "gpt-4o-2024-08-06" {
		t.Errorf("Model = %q, want gpt-4o-2024-08-06", oc.Model)
	}
	if oc.Usage != (Usage{Input: 44, Cached: 16, Output: 12, Reasoning: 5}) {
		t.Errorf("Usage = %+v, want {44 16 12 5}", oc.Usage)
	}
	if len(oc.OutputItems) != 1 {
		t.Fatalf("len(OutputItems) = %d, want 1", len(oc.OutputItems))
	}
	if !oc.Completed {
		t.Error("Completed = false, want true")
	}
	if !json.Valid(oc.OutputItems[0]) {
		t.Errorf("OutputItems[0] is not valid JSON: %s", oc.OutputItems[0])
	}
}

func TestParseJSONChat(t *testing.T) {
	oc := ParseJSONResponse("chat", readFixture(t, "chat.json"))
	if oc.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini", oc.Model)
	}
	if oc.Usage != (Usage{Input: 40, Cached: 8, Output: 10, Reasoning: 3}) {
		t.Errorf("Usage = %+v, want {40 8 10 3}", oc.Usage)
	}
	if len(oc.OutputItems) != 1 {
		t.Fatalf("len(OutputItems) = %d, want 1", len(oc.OutputItems))
	}
	if !oc.Completed {
		t.Error("Completed = false, want true")
	}
	var msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(oc.OutputItems[0], &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Role != "assistant" || msg.Content != "The answer is 42." {
		t.Errorf("message = %+v, want assistant/'The answer is 42.'", msg)
	}
}

func TestLineOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parser panicked on oversized line: %v", r)
		}
	}()
	p := NewStreamParser("responses", time.Now())

	// A single line larger than the 8 MB cap, fed in pieces with no newline.
	big := bytes.Repeat([]byte("x"), lineBufCap+1024)
	for i := 0; i < len(big); i += 4096 {
		end := i + 4096
		if end > len(big) {
			end = len(big)
		}
		_, _ = p.Write(big[i:end])
	}
	_, _ = p.Write([]byte("\n")) // terminate the discarded line

	// The parser must keep processing valid events afterwards.
	tail := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"model":"gpt-4o-2024-08-06","output":[{"id":"m"}],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0}}}}` + "\n\n"
	_, _ = p.Write([]byte(tail))
	oc := p.Finish()

	if oc.ErrorType != "parse_overflow" {
		t.Errorf("ErrorType = %q, want parse_overflow", oc.ErrorType)
	}
	if !oc.Completed {
		t.Error("Completed = false, want true (event after overflow must still parse)")
	}
	if oc.Model != "gpt-4o-2024-08-06" {
		t.Errorf("Model = %q, want gpt-4o-2024-08-06 (parsing continued after overflow)", oc.Model)
	}
}
