package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
)

// sha256Of is a shared test helper (package-level, used across *_test.go).
func sha256Of(b []byte) [32]byte { return sha256.Sum256(b) }

func kinds(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Kind
	}
	return out
}

func eqStrings(a, b []string) bool {
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

func TestSplitRequestResponses(t *testing.T) {
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	body := []byte(`{
		"model":"m",
		"instructions":"sys",
		"tools":[{"type":"function","name":"f"}],
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"user","content":[{"type":"input_text","text":"again"}]}
		]
	}`)

	segs, inputStart, err := SplitRequest("responses", body, r)
	if err != nil {
		t.Fatalf("SplitRequest: %v", err)
	}

	want := []string{"params", "instructions", "tools", "input", "input"}
	if got := kinds(segs); !eqStrings(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if inputStart != 3 {
		t.Fatalf("inputStart = %d, want 3", inputStart)
	}

	// params must retain model but not the split-out keys.
	params := string(segs[0].Data)
	if !strings.Contains(params, `"model":"m"`) {
		t.Fatalf("params missing model: %s", params)
	}
	for _, k := range []string{`"instructions"`, `"tools"`, `"input"`} {
		if strings.Contains(params, k) {
			t.Fatalf("params should not contain %s: %s", k, params)
		}
	}

	// Determinism: identical input element JSON yields identical Hash across
	// two independent calls.
	segs2, _, err := SplitRequest("responses", body, r)
	if err != nil {
		t.Fatalf("SplitRequest (2nd): %v", err)
	}
	if segs[3].Hash != segs2[3].Hash {
		t.Fatalf("input hash not stable across calls: %x vs %x", segs[3].Hash, segs2[3].Hash)
	}
	// The two distinct input elements must hash differently.
	if segs[3].Hash == segs[4].Hash {
		t.Fatalf("distinct inputs share a hash unexpectedly")
	}
	// Hash must equal sha256(Data).
	if got := sha256Of(segs[3].Data); got != segs[3].Hash {
		t.Fatalf("Hash != sha256(Data)")
	}
}

func TestSplitRequestChat(t *testing.T) {
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	body := []byte(`{"model":"m","messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}],"temperature":0.5}`)

	segs, inputStart, err := SplitRequest("chat", body, r)
	if err != nil {
		t.Fatalf("SplitRequest: %v", err)
	}

	want := []string{"params", "input", "input"}
	if got := kinds(segs); !eqStrings(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if inputStart != 1 {
		t.Fatalf("inputStart = %d, want 1", inputStart)
	}

	params := string(segs[0].Data)
	if !strings.Contains(params, `"temperature":0.5`) {
		t.Fatalf("params missing temperature: %s", params)
	}
	if strings.Contains(params, `"messages"`) {
		t.Fatalf("params should not contain messages: %s", params)
	}
}

func TestSplitResponse(t *testing.T) {
	r, err := NewRedactor(DefaultRedactionRules())
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	out := Outcome{OutputItems: []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"more"}]}`),
	}}
	segs := SplitResponse(out, r)
	if len(segs) != 2 {
		t.Fatalf("want 2 output segments, got %d", len(segs))
	}
	for _, s := range segs {
		if s.Kind != "output" {
			t.Fatalf("kind = %q, want output", s.Kind)
		}
		if sha256Of(s.Data) != s.Hash {
			t.Fatalf("Hash != sha256(Data)")
		}
		if !bytes.Contains(s.Data, []byte(`"output_text"`)) {
			t.Fatalf("output segment lost content: %s", s.Data)
		}
	}
}
