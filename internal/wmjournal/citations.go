package wmjournal

import (
	"regexp"
	"strconv"

	"aiseat/internal/workrecords"
)

var sourceCitation = regexp.MustCompile("(?i)source[_ ]id[`* :：=]*([0-9]+)")

// CitationsKnown checks only that explicitly labeled source IDs belong to
// this batch. It does not claim that a cited source proves the text.
func CitationsKnown(output string, batch workrecords.EvidenceBatch) bool {
	allowed := map[int64]bool{}
	for _, p := range batch.Parts {
		for _, o := range p.Occurrences {
			allowed[o.SourceID] = true
		}
	}
	matches := sourceCitation.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || !allowed[id] {
			return false
		}
	}
	return true
}
