package workrecords

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanKeepsRepeatedOccurrencesAndSessionPrefix(t *testing.T) {
	makeSeg := func(s string) Segment { return Segment{Hash: sha256.Sum256([]byte(s)), Kind: "input", Data: []byte(s)} }
	a, b := makeSeg(strings.Repeat("😀\n", 2000)), makeSeg("重复的独立事件")
	sid := int64(7)
	sid2 := int64(8)
	records := []SourceRecord{
		{SourceID: 1, EmployeeID: 1, SessionID: &sid, Record: Record{Request: []Segment{a}, Response: []Segment{b}}},
		{SourceID: 2, EmployeeID: 1, SessionID: &sid, Record: Record{Request: []Segment{a, b, b}}},
		{SourceID: 3, EmployeeID: 1, SessionID: &sid2, Record: Record{Request: []Segment{a}}},
	}
	batches, err := Plan(records, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var a1, a3 strings.Builder
	seenB, reuse := 0, false
	for _, batch := range batches {
		data, _ := json.Marshal(batch)
		if len(data) > 4096 {
			t.Fatalf("oversized batch %d", len(data))
		}
		for _, p := range batch.Parts {
			if p.ReusedFrom == 1 && p.SourceID == 2 && p.ReusedSegments == 1 {
				reuse = true
			}
			if p.SourceID == 1 && p.Direction == "request" {
				a1.WriteString(p.Text)
			}
			if p.SourceID == 3 && p.Direction == "request" {
				a3.WriteString(p.Text)
			}
			if p.SourceID == 2 && p.Text == string(b.Data) {
				seenB++
			}
		}
	}
	if !reuse || a1.String() != string(a.Data) || a3.String() != string(a.Data) || seenB != 2 {
		t.Fatalf("lost content or occurrence: reuse=%v b=%d", reuse, seenB)
	}
}
