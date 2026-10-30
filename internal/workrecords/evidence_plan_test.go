package workrecords

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEvidencePreservesEveryOccurrenceAndHydratesWithinBound(t *testing.T) {
	data := []byte(strings.Repeat("工作😀\n", 1700))
	hash := sha256.Sum256(data)
	key := fmt.Sprintf("%x", hash)
	sid := int64(9)
	occurrences := make([]EvidenceOccurrence, 53)
	for i := range occurrences {
		occurrences[i] = EvidenceOccurrence{SourceID: int64(1 + i/3), Ordinal: i % 3, Time: time.Date(2026, 10, 29, 1, 2, i, 0, time.UTC)}
	}
	group := EvidenceGroup{EmployeeID: 1, EmployeeName: "成员", SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: occurrences}
	var batches []EvidenceBatch
	p, err := NewEvidencePlanner(4096, func(b EvidenceBatch) error { batches = append(batches, b); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Add(group); err != nil {
		t.Fatal(err)
	}
	if err = p.Finish(); err != nil {
		t.Fatal(err)
	}
	restored := map[string]*strings.Builder{}
	metadata := map[string]time.Time{}
	for _, batch := range batches {
		for _, part := range batch.Parts {
			if part.Text != "" {
				t.Fatal("durable manifest contains body")
			}
		}
		raw, e := RestoreEvidence(batch, map[string][]byte{key: data}, 4096)
		if e != nil {
			t.Fatal(e)
		}
		if len(raw) > 4096 {
			t.Fatal("over limit")
		}
		var decoded EvidenceBatch
		if e = json.Unmarshal(raw, &decoded); e != nil {
			t.Fatal(e)
		}
		for _, part := range decoded.Parts {
			for _, occ := range part.Occurrences {
				k := fmt.Sprintf("%d/%d", occ.SourceID, occ.Ordinal)
				b := restored[k]
				if b == nil {
					b = &strings.Builder{}
					restored[k] = b
				}
				b.WriteString(part.Text)
				metadata[k] = occ.Time
			}
		}
	}
	if len(restored) != len(occurrences) {
		t.Fatalf("got %d positions want %d", len(restored), len(occurrences))
	}
	for _, occ := range occurrences {
		k := fmt.Sprintf("%d/%d", occ.SourceID, occ.Ordinal)
		b := restored[k]
		if b.String() != string(data) || !metadata[k].Equal(occ.Time) {
			t.Fatalf("lost content or time for %s", k)
		}
	}
	if _, err = RestoreEvidence(batches[0], map[string][]byte{key: []byte("tampered")}, 4096); err == nil {
		t.Fatal("accepted altered blob")
	}
	if _, err = RestoreEvidence(batches[0], nil, 4096); err == nil {
		t.Fatal("accepted missing blob")
	}
	invalid := batches[0]
	invalid.Parts = append([]EvidencePart(nil), invalid.Parts...)
	invalid.Parts[0].Start = 1
	if _, err = RestoreEvidence(invalid, map[string][]byte{key: data}, 4096); err == nil {
		t.Fatal("accepted invalid UTF-8 byte boundary")
	}

}

func TestEvidenceKeepsIdentityAndDirectionSeparate(t *testing.T) {
	data := []byte("重复的工作")
	hash := sha256.Sum256(data)
	sid := int64(4)
	var batches []EvidenceBatch
	p, _ := NewEvidencePlanner(4096, func(b EvidenceBatch) error { batches = append(batches, b); return nil })
	for _, g := range []EvidenceGroup{
		{EmployeeID: 1, SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: 1, Ordinal: 0}, {SourceID: 1, Ordinal: 1}}},
		{EmployeeID: 2, SessionID: &sid, Direction: "request", Segment: Segment{Kind: "input", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: 2}}},
		{EmployeeID: 1, SessionID: &sid, Direction: "response", Segment: Segment{Kind: "output", Hash: hash, Data: data}, Occurrences: []EvidenceOccurrence{{SourceID: 3}}},
	} {
		if err := p.Add(g); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Finish(); err != nil {
		t.Fatal(err)
	}
	var parts []EvidencePart
	for _, b := range batches {
		parts = append(parts, b.Parts...)
	}
	if len(parts) != 3 || parts[0].EmployeeID != 1 || len(parts[0].Occurrences) != 2 || parts[1].EmployeeID != 2 || parts[2].Direction != "response" {
		t.Fatalf("crossed boundary: %+v", parts)
	}
}
