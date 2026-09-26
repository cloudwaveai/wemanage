// Package capture parses OpenAI-style "responses" and "chat completions"
// payloads, both streaming (SSE) and non-streaming (JSON), into a normalized
// Outcome that records the model, token usage, produced output items and
// time-to-first-token.
//
// NOTE ON DEPENDENCIES: the module task described github.com/tidwall/gjson as
// already present in go.mod, but it is not declared there (only jackc/pgx and
// pressly/goose are). Adding it would require editing go.mod, which is
// explicitly disallowed, and importing it without a go.mod/go.sum entry breaks
// the build under Go's default -mod=readonly. To keep `go build` / `go test`
// green without modifying go.mod, JSON parsing here uses the standard library
// (encoding/json), which the task permits for everything other than gjson.
package capture

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Usage holds token counts extracted from a response.
type Usage struct{ Input, Cached, Output, Reasoning int64 }

// Outcome is the normalized result of parsing a response payload.
type Outcome struct {
	Model       string
	Usage       Usage
	OutputItems []json.RawMessage // responses: the output[] array items; chat: exactly one assembled assistant message object
	TTFT        time.Duration
	HasTTFT     bool
	ErrorType   string
	Completed   bool
}

// lineBufCap caps the internal per-line buffer. A single line exceeding this is
// discarded (ErrorType set to "parse_overflow") rather than growing unbounded.
const lineBufCap = 8 << 20 // 8 MB

// StreamParser incrementally parses a streamed (SSE) response. It implements
// io.Writer so it can sit on an io.TeeReader mirroring the upstream stream, and
// tolerates arbitrary chunk boundaries by buffering incomplete lines.
type StreamParser struct {
	endpoint string
	start    time.Time

	// line assembly
	line       []byte // partial (not yet newline-terminated) line bytes
	discarding bool   // true while dropping the remainder of an over-long line

	// shared results
	model     string
	ttft      time.Duration
	hasTTFT   bool
	errorType string
	completed bool

	// responses-specific
	curEvent       string            // current SSE "event:" type for the block being assembled
	curData        []byte            // accumulated "data:" payload for the current block
	respUsage      Usage             // usage from the terminal response object
	completedItems []json.RawMessage // response.output[] items from the terminal event
	fallbackItems  []json.RawMessage // items collected from response.output_item.done events

	// chat-specific
	chatContent strings.Builder
	chatTools   map[int]*toolCallAcc
	chatUsage   Usage
	haveUsage   bool
}

type toolCallAcc struct {
	id, name, typ string
	args          strings.Builder
}

// NewStreamParser returns a parser for the given endpoint ("responses" or
// "chat"). start is used to compute TTFT.
func NewStreamParser(endpoint string, start time.Time) *StreamParser {
	return &StreamParser{endpoint: endpoint, start: start}
}

// Write consumes a chunk of the mirrored stream. It never returns a short write
// and never errors, satisfying io.Writer for use with io.TeeReader. Bytes may be
// split anywhere, including mid-line and mid-UTF8; incomplete lines are buffered.
func (p *StreamParser) Write(b []byte) (int, error) {
	total := len(b)
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			// No newline in the remaining bytes: buffer or discard.
			if p.discarding {
				return total, nil
			}
			if len(p.line)+len(b) > lineBufCap {
				p.errorType = "parse_overflow"
				p.line = p.line[:0]
				p.discarding = true
				return total, nil
			}
			p.line = append(p.line, b...)
			return total, nil
		}

		chunk := b[:i]
		b = b[i+1:]

		if p.discarding {
			// The newline ends the over-long line we were dropping.
			p.discarding = false
			p.line = p.line[:0]
			continue
		}
		if len(p.line)+len(chunk) > lineBufCap {
			// The completed line is too big: drop it but keep going.
			p.errorType = "parse_overflow"
			p.line = p.line[:0]
			continue
		}
		p.line = append(p.line, chunk...)
		p.processLine(p.line)
		p.line = p.line[:0]
	}
	return total, nil
}

// Finish flushes any pending state and returns the parsed Outcome. Safe to call
// once after the stream ends.
func (p *StreamParser) Finish() Outcome {
	if p.endpoint == "chat" {
		return p.finishChat()
	}
	// Flush a pending, fully-buffered event block that was not terminated by a
	// trailing blank line.
	if len(p.curData) > 0 {
		p.dispatchRespBlock()
		p.curData = p.curData[:0]
		p.curEvent = ""
	}
	items := p.completedItems
	if len(items) == 0 {
		items = p.fallbackItems
	}
	return Outcome{
		Model:       p.model,
		Usage:       p.respUsage,
		OutputItems: items,
		TTFT:        p.ttft,
		HasTTFT:     p.hasTTFT,
		ErrorType:   p.errorType,
		Completed:   p.completed,
	}
}

func (p *StreamParser) finishChat() Outcome {
	asm := chatAssembled{Role: "assistant", Content: p.chatContent.String()}
	if len(p.chatTools) > 0 {
		idxs := make([]int, 0, len(p.chatTools))
		for k := range p.chatTools {
			idxs = append(idxs, k)
		}
		sort.Ints(idxs)
		for _, k := range idxs {
			tc := p.chatTools[k]
			typ := tc.typ
			if typ == "" {
				typ = "function"
			}
			asm.ToolCalls = append(asm.ToolCalls, chatToolCall{
				ID:       tc.id,
				Type:     typ,
				Function: chatToolFunc{Name: tc.name, Arguments: tc.args.String()},
			})
		}
	}
	raw, _ := json.Marshal(asm)
	usage := Usage{}
	if p.haveUsage {
		usage = p.chatUsage
	}
	return Outcome{
		Model:       p.model,
		Usage:       usage,
		OutputItems: []json.RawMessage{raw},
		TTFT:        p.ttft,
		HasTTFT:     p.hasTTFT,
		ErrorType:   p.errorType,
		Completed:   p.completed,
	}
}

func (p *StreamParser) processLine(raw []byte) {
	line := trimCR(raw)
	if p.endpoint == "chat" {
		p.processChatLine(line)
		return
	}
	p.processRespLine(line)
}

// ---- responses SSE ----

func (p *StreamParser) processRespLine(line []byte) {
	if len(line) == 0 {
		// Blank line terminates the current event block.
		p.dispatchRespBlock()
		p.curEvent = ""
		p.curData = p.curData[:0]
		return
	}
	if line[0] == ':' {
		return // comment
	}
	if v, ok := fieldValue(line, "event:"); ok {
		p.curEvent = string(bytes.TrimSpace(v))
		return
	}
	if v, ok := fieldValue(line, "data:"); ok {
		if len(p.curData) > 0 {
			p.curData = append(p.curData, '\n')
		}
		p.curData = append(p.curData, v...)
		return
	}
	// Other SSE fields (id:, retry:, ...) are ignored.
}

type respUsageJSON struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type respObjJSON struct {
	Model  string            `json:"model"`
	Usage  respUsageJSON     `json:"usage"`
	Output []json.RawMessage `json:"output"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type respEventJSON struct {
	Type     string          `json:"type"`
	Response *respObjJSON    `json:"response"`
	Item     json.RawMessage `json:"item"`
}

func (p *StreamParser) dispatchRespBlock() {
	if len(p.curData) == 0 {
		return
	}
	var d respEventJSON
	_ = json.Unmarshal(p.curData, &d) // malformed JSON leaves zero values

	typ := p.curEvent
	if typ == "" {
		typ = d.Type
	}

	if strings.HasSuffix(typ, ".delta") && !p.hasTTFT {
		p.hasTTFT = true
		p.ttft = time.Since(p.start)
	}

	switch typ {
	case "response.output_item.done":
		if len(d.Item) > 0 {
			p.fallbackItems = append(p.fallbackItems, cloneRaw(d.Item))
		}
	case "response.completed", "response.incomplete", "response.failed":
		if d.Response != nil {
			p.model = d.Response.Model
			p.respUsage = usageFromResp(d.Response.Usage)
			p.completedItems = p.completedItems[:0]
			for _, it := range d.Response.Output {
				p.completedItems = append(p.completedItems, cloneRaw(it))
			}
			if typ == "response.failed" {
				code := ""
				if d.Response.Error != nil {
					code = d.Response.Error.Code
				}
				if code == "" {
					code = "failed"
				}
				p.errorType = code
			}
		} else if typ == "response.failed" {
			p.errorType = "failed"
		}
		p.completed = true
	}
}

func usageFromResp(u respUsageJSON) Usage {
	return Usage{
		Input:     u.InputTokens,
		Cached:    u.InputTokensDetails.CachedTokens,
		Output:    u.OutputTokens,
		Reasoning: u.OutputTokensDetails.ReasoningTokens,
	}
}

// ---- chat completions SSE ----

type chatUsageJSON struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type chatDeltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatChunkJSON struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string              `json:"content"`
			ReasoningContent string              `json:"reasoning_content"`
			ToolCalls        []chatDeltaToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *chatUsageJSON `json:"usage"`
}

// assembled chat output types
type chatAssembled struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *StreamParser) processChatLine(line []byte) {
	if len(line) == 0 || line[0] == ':' {
		return
	}
	v, ok := fieldValue(line, "data:")
	if !ok {
		return // chat streams only carry data: lines we care about
	}
	if string(bytes.TrimSpace(v)) == "[DONE]" {
		p.completed = true
		return
	}
	var c chatChunkJSON
	if err := json.Unmarshal(v, &c); err != nil {
		return // never panic on malformed JSON
	}
	if p.model == "" && c.Model != "" {
		p.model = c.Model
	}
	if len(c.Choices) > 0 {
		d := c.Choices[0].Delta
		if !p.hasTTFT && (d.Content != "" || d.ReasoningContent != "" || len(d.ToolCalls) > 0) {
			p.hasTTFT = true
			p.ttft = time.Since(p.start)
		}
		if d.Content != "" {
			p.chatContent.WriteString(d.Content)
		}
		for _, tc := range d.ToolCalls {
			acc := p.toolCall(tc.Index)
			if acc.id == "" && tc.ID != "" {
				acc.id = tc.ID
			}
			if acc.name == "" && tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			if acc.typ == "" && tc.Type != "" {
				acc.typ = tc.Type
			}
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if c.Usage != nil {
		p.chatUsage = Usage{
			Input:     c.Usage.PromptTokens,
			Cached:    c.Usage.PromptTokensDetails.CachedTokens,
			Output:    c.Usage.CompletionTokens,
			Reasoning: c.Usage.CompletionTokensDetails.ReasoningTokens,
		}
		p.haveUsage = true
	}
}

func (p *StreamParser) toolCall(index int) *toolCallAcc {
	if p.chatTools == nil {
		p.chatTools = make(map[int]*toolCallAcc)
	}
	if acc, ok := p.chatTools[index]; ok {
		return acc
	}
	acc := &toolCallAcc{}
	p.chatTools[index] = acc
	return acc
}

// ---- non-streaming JSON ----

type chatJSONBody struct {
	Model   string         `json:"model"`
	Usage   *chatUsageJSON `json:"usage"`
	Choices []struct {
		Message json.RawMessage `json:"message"`
	} `json:"choices"`
}

// ParseJSONResponse parses a complete (non-streaming) JSON body.
func ParseJSONResponse(endpoint string, body []byte) Outcome {
	if endpoint == "chat" {
		var j chatJSONBody
		_ = json.Unmarshal(body, &j)
		u := Usage{}
		if j.Usage != nil {
			u = Usage{
				Input:     j.Usage.PromptTokens,
				Cached:    j.Usage.PromptTokensDetails.CachedTokens,
				Output:    j.Usage.CompletionTokens,
				Reasoning: j.Usage.CompletionTokensDetails.ReasoningTokens,
			}
		}
		var items []json.RawMessage
		if len(j.Choices) > 0 && len(j.Choices[0].Message) > 0 {
			items = []json.RawMessage{cloneRaw(j.Choices[0].Message)}
		}
		return Outcome{Model: j.Model, Usage: u, OutputItems: items, Completed: true}
	}

	var r respObjJSON
	_ = json.Unmarshal(body, &r)
	var items []json.RawMessage
	for _, it := range r.Output {
		items = append(items, cloneRaw(it))
	}
	return Outcome{
		Model:       r.Model,
		Usage:       usageFromResp(r.Usage),
		OutputItems: items,
		Completed:   true,
	}
}

// ---- helpers ----

func trimCR(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return b[:n-1]
	}
	return b
}

// fieldValue reports whether line begins with prefix and, if so, returns the
// value after the prefix with a single optional leading space stripped.
func fieldValue(line []byte, prefix string) ([]byte, bool) {
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return nil, false
	}
	v := line[len(prefix):]
	if len(v) > 0 && v[0] == ' ' {
		v = v[1:]
	}
	return v, true
}

func cloneRaw(src json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), src...)
}
