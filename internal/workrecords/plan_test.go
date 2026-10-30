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

func TestPlanReusesOnlyInitialInputOfSameOwnerAndSession(t *testing.T) {
	seg := func(kind, value string) Segment {
		return Segment{Kind: kind, Hash: sha256.Sum256([]byte(value)), Data: []byte(value)}
	}
	sid := int64(10)
	params1, params2 := seg("params", "params1"), seg("params", "params2")
	a, b, c := seg("input", "a"), seg("input", "b"), seg("input", "c")
	records := []SourceRecord{
		{SourceID: 1, EmployeeID: 1, SessionID: &sid, Record: Record{Request: []Segment{params1, a, b}}},
		{SourceID: 2, EmployeeID: 1, SessionID: &sid, Record: Record{Request: []Segment{params2, a, b, c, c}}},
		{SourceID: 3, EmployeeID: 2, SessionID: &sid, Record: Record{Request: []Segment{params2, a, b}}},
		{SourceID: 4, EmployeeID: 1, SessionID: &sid, Record: Record{Request: []Segment{params2, a, c, b}}},
	}
	batches, err := Plan(records, 4096)
	if err != nil {
		t.Fatal(err)
	}
	parts := map[int64][]PlannedPart{}
	for _, batch := range batches {
		for _, p := range batch.Parts {
			parts[p.SourceID] = append(parts[p.SourceID], p)
		}
	}
	check := func(source int64, from int64, start, previous, count int) {
		t.Helper()
		found := false
		for _, p := range parts[source] {
			if p.ReusedSegments > 0 {
				if p.ReusedFrom != from || p.ReusedStart != start || p.ReusedPreviousStart != previous || p.ReusedSegments != count {
					t.Fatalf("incorrect reference: %+v", p)
				}
				found = true
			}
		}
		if (count > 0) != found {
			t.Fatalf("source %d reference = %v", source, found)
		}
	}
	check(2, 1, 1, 1, 2)
	check(3, 0, 0, 0, 0)
	check(4, 2, 1, 1, 1)
	var values []string
	for _, p := range parts[2] {
		if p.ReusedSegments == 0 {
			values = append(values, p.Text)
		}
	}
	if strings.Join(values, ",") != "params2,c,c" {
		t.Fatalf("lost or repeated input: %v", values)
	}
}
