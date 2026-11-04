package workrecords

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCompactEvidenceRetainsPositionsTimesAndScope(t *testing.T) {
	at := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	sid := int64(7)
	part := EvidencePart{EmployeeID: 1, EmployeeName: "成员", SessionID: &sid, Direction: "request", Kind: "input", Hash: strings.Repeat("a", 64), Start: 0, End: 3, Total: 3, Text: "abc"}
	for i := 0; i < 500; i++ {
		part.Occurrences = append(part.Occurrences, EvidenceOccurrence{SourceID: int64(1 + i/100), Ordinal: i % 100, Time: at.Add(time.Duration(i/100) * time.Minute)})
	}
	old, _ := json.Marshal(EvidenceBatch{Format: evidenceFormat, Parts: []EvidencePart{part}})
	batch, e := compactEvidence([]EvidencePart{part}, true)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(batch)
	if len(raw) >= len(old) {
		t.Fatalf("not compact: %d >= %d", len(raw), len(old))
	}
	if len(batch.Sources) != 5 || len(batch.Parts) != 1 || len(batch.Parts[0].Occurrences) != 500 || batch.Parts[0].Text != "abc" || batch.Parts[0].EmployeeID != 1 || *batch.Parts[0].SessionID != sid {
		t.Fatal("metadata lost")
	}
	times := map[int64]time.Time{}
	for _, s := range batch.Sources {
		times[s.ID] = s.Time
	}
	for i, ref := range batch.Parts[0].Occurrences {
		before := part.Occurrences[i]
		if ref[0] != before.SourceID || ref[1] != int64(before.Ordinal) || !times[ref[0]].Equal(before.Time) {
			t.Fatalf("position or time lost: %d", i)
		}
	}
	durable, e := compactEvidence([]EvidencePart{part}, false)
	if e != nil {
		t.Fatal(e)
	}
	if durable.Parts[0].Text != "" {
		t.Fatal("duplicated text in durable manifest")
	}
	part.Occurrences[1].Time = at.Add(time.Hour)
	if _, e = compactEvidence([]EvidencePart{part}, true); e == nil {
		t.Fatal("accepted inconsistent source time")
	}
}
