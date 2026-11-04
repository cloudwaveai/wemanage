package workrecords

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOpaqueReasoningRetainsEveryOriginalByteWithoutSendingCiphertext(t *testing.T) {
	secret := strings.Repeat("opaque-encrypted-", 1200)
	data := []byte(`{"type":"reasoning","summary":[{"text":"可读的摘要"}],"encrypted_content":"` + secret + `","tail":"保留"}`)
	hash := sha256.Sum256(data)
	key := fmt.Sprintf("%x", hash)
	sid := int64(5)
	group := EvidenceGroup{EmployeeID: 1, SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: 1, Ordinal: 0}, {SourceID: 2, Ordinal: 3}}}
	var batches []EvidenceBatch
	p, _ := NewEvidencePlanner(4096, func(b EvidenceBatch) error { batches = append(batches, b); return nil })
	if e := p.enableOpaquePrototype(); e != nil {
		t.Fatal(e)
	}
	if e := p.Add(group); e != nil {
		t.Fatal(e)
	}
	if e := p.Finish(); e != nil {
		t.Fatal(e)
	}
	var restored []byte
	opaque := 0
	var opaquePart EvidencePart
	for _, batch := range batches {
		payload, e := RestoreEvidence(batch, map[string][]byte{key: data}, 4096)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(payload), secret[:64]) {
			t.Fatal("ciphertext sent to text model")
		}
		var actual EvidenceBatch
		if e = json.Unmarshal(payload, &actual); e != nil {
			t.Fatal(e)
		}
		for _, part := range actual.Parts {
			if len(part.Occurrences) != 2 {
				t.Fatal("occurrence lost")
			}
			if part.Start != len(restored) {
				t.Fatalf("gap or out of order %d != %d", part.Start, len(restored))
			}
			restored = append(restored, data[part.Start:part.End]...)
			if part.Opaque {
				opaque++
				opaquePart = part
				if !strings.Contains(part.Text, opaqueReasoningText) {
					t.Fatal("missing opaque marker")
				}
			}
		}
	}
	if string(restored) != string(data) || opaque != 1 {
		t.Fatalf("source not reconstructable: bytes=%d opaque=%d", len(restored), opaque)
	}
	opaquePart.Opaque = false
	if _, e := RestoreEvidence(EvidenceBatch{Format: opaquePrototypeFormat, Parts: []EvidencePart{opaquePart}}, map[string][]byte{key: data}, 4096); e == nil {
		t.Fatal("accepted plaintext ciphertext range")
	}
	opaquePart.Opaque = true
	opaquePart.End--
	if _, e := RestoreEvidence(EvidenceBatch{Format: opaquePrototypeFormat, Parts: []EvidencePart{opaquePart}}, map[string][]byte{key: data}, 4096); e == nil {
		t.Fatal("accepted partial opaque range")
	}
}
func TestOpaqueReasoningDoesNotHideOtherFields(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{"type":"message","encrypted_content":"visible"}`), []byte(`{"type":"reasoning","summary":[{"text":"visible"}]}`)} {
		if _, _, ok, e := reasoningCipherRange(data); e != nil || ok {
			t.Fatalf("hid non-cipher field: %v %v", ok, e)
		}
	}
	if _, _, _, e := reasoningCipherRange([]byte(`{"type":"reasoning","encrypted_content":123}`)); e == nil {
		t.Fatal("accepted unsupported cipher type")
	}
}
