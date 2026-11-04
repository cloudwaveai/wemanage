package wmjournal

import (
	"aiseat/internal/workrecords"
	"testing"
)

func TestCitationsKnown(t *testing.T) {
	b := workrecords.EvidenceBatch{Parts: []workrecords.EvidencePart{{Occurrences: []workrecords.EvidenceOccurrence{{SourceID: 12}, {SourceID: 35}}}}}
	if !CitationsKnown("依据 source_id=12 和 source_id **35**", b) {
		t.Fatal("valid references rejected")
	}
	if CitationsKnown("没有来源", b) || CitationsKnown("source_id:12；source_id=999", b) {
		t.Fatal("unverified references accepted")
	}
}
