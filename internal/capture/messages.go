package capture

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Anthropic Messages ("messages" endpoint, e.g. Claude Code). The stream is
// SSE like Responses: message_start carries the model and input usage,
// content_block_* build the reply, message_delta the output usage. The reply
// is kept as one assistant message, like chat.

type msgUsageJSON struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type msgEventJSON struct {
	Type    string `json:"type"`
	Message *struct {
		Model string        `json:"model"`
		Usage *msgUsageJSON `json:"usage"`
	} `json:"message"`
	Index        int             `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage *msgUsageJSON `json:"usage"`
	Error *struct {
		Type string `json:"type"`
	} `json:"error"`
}

type msgBlockAcc struct {
	start json.RawMessage // content_block from content_block_start
	typ   string
	text  strings.Builder // text / thinking
	input strings.Builder // tool_use partial JSON
}

// msgState is the messages part of a StreamParser.
type msgState struct {
	blocks map[int]*msgBlockAcc
	usage  msgUsageJSON
}

// msgUsage maps Anthropic usage onto Usage: input counts cached and newly
// cached tokens too, as OpenAI's does.
func msgUsage(u msgUsageJSON) Usage {
	return Usage{
		Input:  u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		Cached: u.CacheReadInputTokens,
		Output: u.OutputTokens,
	}
}

func (s *msgState) mergeUsage(u *msgUsageJSON) {
	if u == nil {
		return
	}
	if u.InputTokens > 0 || u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
		s.usage.InputTokens, s.usage.CacheReadInputTokens, s.usage.CacheCreationInputTokens =
			u.InputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens
	}
	if u.OutputTokens > 0 {
		s.usage.OutputTokens = u.OutputTokens
	}
}

func (p *StreamParser) dispatchMsgBlock() {
	if len(p.curData) == 0 {
		return
	}
	var d msgEventJSON
	_ = json.Unmarshal(p.curData, &d)
	typ := p.curEvent
	if typ == "" {
		typ = d.Type
	}
	if p.msg.blocks == nil {
		p.msg.blocks = map[int]*msgBlockAcc{}
	}
	switch typ {
	case "message_start":
		if d.Message != nil {
			p.model = d.Message.Model
			p.msg.mergeUsage(d.Message.Usage)
		}
	case "content_block_start":
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(d.ContentBlock, &head)
		p.msg.blocks[d.Index] = &msgBlockAcc{start: cloneRaw(d.ContentBlock), typ: head.Type}
	case "content_block_delta":
		if !p.hasTTFT {
			p.hasTTFT = true
			p.ttft = time.Since(p.start)
		}
		b := p.msg.blocks[d.Index]
		if b == nil {
			b = &msgBlockAcc{typ: "text"}
			p.msg.blocks[d.Index] = b
		}
		switch d.Delta.Type {
		case "text_delta":
			b.text.WriteString(d.Delta.Text)
		case "thinking_delta":
			b.text.WriteString(d.Delta.Thinking)
		case "input_json_delta":
			b.input.WriteString(d.Delta.PartialJSON)
		}
	case "message_delta":
		p.msg.mergeUsage(d.Usage)
	case "message_stop":
		p.completed = true
	case "error":
		p.errorType = "failed"
		if d.Error != nil && d.Error.Type != "" {
			p.errorType = d.Error.Type
		}
		p.completed = true
	}
}

func (p *StreamParser) finishMessages() Outcome {
	if len(p.curData) > 0 {
		p.dispatchMsgBlock()
		p.curData = p.curData[:0]
		p.curEvent = ""
	}
	idxs := make([]int, 0, len(p.msg.blocks))
	for k := range p.msg.blocks {
		idxs = append(idxs, k)
	}
	sort.Ints(idxs)
	content := make([]map[string]any, 0, len(idxs))
	for _, i := range idxs {
		b := p.msg.blocks[i]
		block := map[string]any{}
		_ = json.Unmarshal(b.start, &block)
		if block["type"] == nil {
			block["type"] = b.typ
		}
		switch b.typ {
		case "text":
			block["text"] = b.text.String()
		case "thinking":
			block["thinking"] = b.text.String()
		case "tool_use", "server_tool_use":
			if in := b.input.String(); in != "" {
				block["input"] = json.RawMessage(in)
				if !json.Valid([]byte(in)) {
					block["input"] = in
				}
			}
		}
		content = append(content, block)
	}
	var items []json.RawMessage
	if len(content) > 0 {
		raw, _ := json.Marshal(map[string]any{"role": "assistant", "content": content})
		items = []json.RawMessage{raw}
	}
	return Outcome{
		Model: p.model, Usage: msgUsage(p.msg.usage), OutputItems: items,
		TTFT: p.ttft, HasTTFT: p.hasTTFT, ErrorType: p.errorType, Completed: p.completed,
	}
}

// parseMessagesJSON parses a non-streaming Messages reply.
func parseMessagesJSON(body []byte) Outcome {
	var j struct {
		Model   string            `json:"model"`
		Content []json.RawMessage `json:"content"`
		Usage   msgUsageJSON      `json:"usage"`
	}
	_ = json.Unmarshal(body, &j)
	var items []json.RawMessage
	if len(j.Content) > 0 {
		raw, _ := json.Marshal(map[string]any{"role": "assistant", "content": j.Content})
		items = []json.RawMessage{raw}
	}
	return Outcome{Model: j.Model, Usage: msgUsage(j.Usage), OutputItems: items, Completed: true}
}
