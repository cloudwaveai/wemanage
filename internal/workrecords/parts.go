package workrecords

import (
	"errors"
	"unicode/utf8"
)

// Part describes a contiguous, reversible slice of one stored segment.
// Byte offsets, hash and sequence make it possible to audit and reassemble
// the exact source, including a segment larger than the model input limit.
type Part struct {
	Hash              [32]byte
	Kind              string
	Start, End, Total int
	Text              string
}

// Split divides rather than truncates. The payload is always valid UTF-8, and
// concatenating its parts in order yields the original bytes exactly.
func Split(segment Segment, maxBytes int) ([]Part, error) {
	if maxBytes < utf8.UTFMax || !utf8.Valid(segment.Data) {
		return nil, errors.New("invalid segment or part size")
	}
	if len(segment.Data) == 0 {
		return []Part{{Hash: segment.Hash, Kind: segment.Kind}}, nil
	}
	var result []Part
	for start := 0; start < len(segment.Data); {
		end := min(start+maxBytes, len(segment.Data))
		for end < len(segment.Data) && !utf8.RuneStart(segment.Data[end]) {
			end--
		}
		if end <= start {
			return nil, errors.New("part size cannot hold rune")
		}
		result = append(result, Part{Hash: segment.Hash, Kind: segment.Kind, Start: start, End: end, Total: len(segment.Data), Text: string(segment.Data[start:end])})
		start = end
	}
	return result, nil
}
