package wmjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiseat/internal/workrecords"
)

func TestMaterializeOriginalAndOpaque(t *testing.T) {
	data := []byte(`{"type":"reasoning","summary":"已测试","encrypted_content":"sensitivecipher","tail":"待验证"}`)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	p := workrecords.EvidencePart{Hash: hash, Start: 0, End: len(data), Total: len(data), Occurrences: []workrecords.EvidenceOccurrence{{SourceID: 1}}}
	b := workrecords.EvidenceBatch{Format: "session-evidence-v1", Parts: []workrecords.EvidencePart{p}}
	got, gap, err := Materialize(b, map[string][]byte{hash: data}, 10000)
	if err != nil || gap || strings.Contains(string(got), "sensitivecipher") || !strings.Contains(string(got), "已测试") || !strings.Contains(string(got), "待验证") {
		t.Fatalf("%s %v %v", got, gap, err)
	}
	if _, _, err = Materialize(b, map[string][]byte{hash: []byte("tampered")}, 10000); err == nil {
		t.Fatal("accepted modified source")
	}
}

func TestOpaqueOnlyDoesNotRequireModel(t *testing.T) {
	data := []byte(`{"url":"data:image/png;base64,QUJDREVGRw=="}`)
	spans, _, err := UnreadableSpans(data)
	if err != nil || len(spans) != 1 {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	p := workrecords.EvidencePart{Hash: hash, Start: spans[0].Start, End: spans[0].End, Total: len(data), Direction: "request", Occurrences: []workrecords.EvidenceOccurrence{{SourceID: 9, Ordinal: 2}}}
	batch := workrecords.EvidenceBatch{Format: "session-evidence-v1", Parts: []workrecords.EvidencePart{p}}
	note, only, err := OpaqueOnly(batch, map[string][]byte{hash: data})
	if err != nil || !only || !strings.Contains(note, "source_id 9") || strings.Contains(note, "QUJD") {
		t.Fatalf("%q %v %v", note, only, err)
	}
	batch.Parts[0].Start = 0
	_, only, err = OpaqueOnly(batch, map[string][]byte{hash: data})
	if err != nil || only {
		t.Fatal("readable material treated as opaque")
	}
}

func TestCompactMaterialKeepsOccurrences(t *testing.T) {
	data := []byte("可读内容")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	at := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	p := workrecords.EvidencePart{Hash: hash, Start: 0, End: len(data), Total: len(data), Occurrences: []workrecords.EvidenceOccurrence{{SourceID: 7, Ordinal: 2, Time: at}, {SourceID: 8, Ordinal: 3, Time: at}}}
	got, _, err := Materialize(workrecords.EvidenceBatch{Format: "session-evidence-v1", Parts: []workrecords.EvidencePart{p, p}}, map[string][]byte{hash: data}, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Sources []struct {
			ID   int64     `json:"id"`
			Time time.Time `json:"time"`
		} `json:"sources"`
		Parts []struct {
			Occurrences [][2]int64 `json:"occurrences"`
		} `json:"parts"`
	}
	if err = json.Unmarshal(got, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Sources) != 2 || len(d.Parts) != 2 || d.Parts[1].Occurrences[1] != [2]int64{8, 3} || !d.Sources[0].Time.Equal(at) {
		t.Fatal("source position or time lost")
	}
}
