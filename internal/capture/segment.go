package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Segment is one redacted, compacted piece of a request or response body,
// tagged by kind and content-addressed by the sha256 of its (redacted) data.
type Segment struct {
	Kind string   // "instructions" | "tools" | "input" | "params" | "output"
	Data []byte   // redacted + compact JSON of the element
	Hash [32]byte // sha256(Data)
}

// SplitRequest splits a request body into ordered segments.
//
// endpoint is "responses" or "chat". The order is:
//  1. instructions, if present [kind "instructions"]
//  2. the tools array, if present [kind "tools"]
//  3. each element of input[] (responses) or messages[] (chat) [kind "input"]
//  4. a final params segment [kind "params"] = the body with instructions,
//     tools and input/messages removed.
//
// inputStart is the index within the returned slice of the first input element
// segment (or the index of the params segment when there are no inputs). Each
// segment's Data is redactor.Redact(compactJSON(element)) and its Hash is the
// sha256 of that Data.
func SplitRequest(endpoint string, body []byte, r *Redactor) (segs []Segment, inputStart int, err error) {
	if !gjson.ValidBytes(body) {
		return nil, 0, fmt.Errorf("capture: invalid request JSON")
	}

	arrKey := "input"
	if endpoint == "chat" {
		arrKey = "messages"
	}

	add := func(kind, raw string) error {
		seg, err := makeSegment(kind, raw, r)
		if err != nil {
			return err
		}
		segs = append(segs, seg)
		return nil
	}

	if res := gjson.GetBytes(body, "instructions"); res.Exists() {
		if err := add("instructions", res.Raw); err != nil {
			return nil, 0, err
		}
	}
	if res := gjson.GetBytes(body, "tools"); res.Exists() {
		if err := add("tools", res.Raw); err != nil {
			return nil, 0, err
		}
	}

	inputStart = len(segs)
	if arr := gjson.GetBytes(body, arrKey); arr.IsArray() {
		for _, el := range arr.Array() {
			if err := add("input", el.Raw); err != nil {
				return nil, 0, err
			}
		}
	}

	params := body
	for _, key := range []string{"instructions", "tools", arrKey} {
		params, err = sjson.DeleteBytes(params, key)
		if err != nil {
			return nil, 0, fmt.Errorf("capture: strip %q from params: %w", key, err)
		}
	}
	if err := add("params", string(params)); err != nil {
		return nil, 0, err
	}

	return segs, inputStart, nil
}

// SplitResponse turns an Outcome's OutputItems into redacted, hashed segments,
// each with kind "output".
func SplitResponse(o Outcome, r *Redactor) []Segment {
	var segs []Segment
	for _, item := range o.OutputItems {
		seg, err := makeSegment("output", string(item), r)
		if err != nil {
			// OutputItems are produced from parsed JSON, so this is unexpected;
			// fall back to the raw bytes redacted in place rather than dropping.
			data := r.Redact(append([]byte(nil), item...))
			seg = Segment{Kind: "output", Data: data, Hash: sha256.Sum256(data)}
		}
		segs = append(segs, seg)
	}
	return segs
}

// makeSegment compacts raw JSON, redacts it, and content-addresses the result.
func makeSegment(kind, raw string, r *Redactor) (Segment, error) {
	data, err := compactJSON(raw)
	if err != nil {
		return Segment{}, fmt.Errorf("capture: compact %s segment: %w", kind, err)
	}
	data = r.Redact(data)
	return Segment{Kind: kind, Data: data, Hash: sha256.Sum256(data)}, nil
}

// compactJSON returns raw with insignificant whitespace removed, preserving key
// order and token content so identical elements always compact identically.
func compactJSON(raw string) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(raw)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
