package wmjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"aiseat/internal/workrecords"
)

// Materialize rebuilds one durable v1 manifest from verified original blobs.
// The caller must independently validate each occurrence and hold privacy locks
// until the model result has been persisted.
func Materialize(batch workrecords.EvidenceBatch, blobs map[string][]byte, maxBytes int) ([]byte, bool, error) {
	if batch.Format != "session-evidence-v1" || len(batch.Parts) == 0 {
		return nil, false, errors.New("unsupported manifest")
	}
	gap := false
	type inspected struct {
		spans []Span
		gap   bool
	}
	cache := map[string]inspected{}
	for i := range batch.Parts {
		part := &batch.Parts[i]
		data, ok := blobs[part.Hash]
		if !ok {
			return nil, false, errors.New("missing source")
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != part.Hash || part.Total != len(data) || part.Start < 0 || part.End < part.Start || part.End > len(data) || len(part.Occurrences) == 0 || part.Opaque || !utf8.Valid(data) || !utf8.Valid(data[part.Start:part.End]) {
			return nil, false, errors.New("invalid source reference")
		}
		v, exists := cache[part.Hash]
		if !exists {
			spans, mediaGap, err := UnreadableSpans(data)
			if err != nil {
				return nil, false, err
			}
			v = inspected{spans, mediaGap}
			cache[part.Hash] = v
		}
		gap = gap || v.gap
		part.Text = projectSpans(data, part.Start, part.End, v.spans)
	}
	// Timestamps repeat across many fragments. Store them once in the model
	// payload and keep every occurrence as [source_id, ordinal].
	type source struct {
		ID   int64     `json:"id"`
		Time time.Time `json:"time"`
	}
	type part struct {
		EmployeeID   int64      `json:"employee_id"`
		EmployeeName string     `json:"employee_name,omitempty"`
		SessionID    *int64     `json:"session_id"`
		Direction    string     `json:"direction"`
		Kind         string     `json:"kind"`
		Hash         string     `json:"hash"`
		Start        int        `json:"start"`
		End          int        `json:"end"`
		Total        int        `json:"total"`
		Occurrences  [][2]int64 `json:"occurrences"`
		Text         string     `json:"text"`
	}
	payload := struct {
		Format  string   `json:"format"`
		Sources []source `json:"sources"`
		Parts   []part   `json:"parts"`
	}{Format: "wm-compact-evidence-v1", Sources: []source{}, Parts: []part{}}
	seen := map[int64]time.Time{}
	for _, p := range batch.Parts {
		x := part{EmployeeID: p.EmployeeID, EmployeeName: p.EmployeeName, SessionID: p.SessionID, Direction: p.Direction, Kind: p.Kind, Hash: p.Hash, Start: p.Start, End: p.End, Total: p.Total, Text: p.Text, Occurrences: [][2]int64{}}
		for _, occ := range p.Occurrences {
			if at, exists := seen[occ.SourceID]; exists {
				if !at.Equal(occ.Time) {
					return nil, false, errors.New("inconsistent source time")
				}
			} else {
				seen[occ.SourceID] = occ.Time
				payload.Sources = append(payload.Sources, source{ID: occ.SourceID, Time: occ.Time})
			}
			x.Occurrences = append(x.Occurrences, [2]int64{occ.SourceID, int64(occ.Ordinal)})
		}
		payload.Parts = append(payload.Parts, x)
	}
	output, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	if len(output) > maxBytes {
		return nil, false, errors.New("model input exceeds capacity")
	}
	return output, gap, nil
}

// OpaqueOnly records coverage rather than asking a model to interpret a batch
// consisting entirely of encoded media or ciphertext. The original manifest
// remains the authority for all source references.
func OpaqueOnly(batch workrecords.EvidenceBatch, blobs map[string][]byte) (string, bool, error) {
	var out strings.Builder
	cache := map[string][]Span{}
	out.WriteString("[覆盖缺口：此批仅包含不可直接读取的媒体编码或加密字段；未调用模型，原文及来源位置已保留。]\n")
	for _, part := range batch.Parts {
		data, ok := blobs[part.Hash]
		if !ok {
			return "", false, errors.New("missing source")
		}
		spans, exists := cache[part.Hash]
		if !exists {
			var err error
			spans, _, err = UnreadableSpans(data)
			if err != nil {
				return "", false, err
			}
			cache[part.Hash] = spans
		}
		covered := false
		for _, s := range spans {
			if part.Start < s.End && part.End > s.Start && part.End > part.Start {
				left, right := max(part.Start, s.Start), min(part.End, s.End)
				// JSON punctuation around an opaque value contains no work
				// information. Any other bytes remain model input.
				structural := " \t\r\n{}[],:\""
				if len(bytes.Trim(data[part.Start:left], structural)) != 0 || len(bytes.Trim(data[right:part.End], structural)) != 0 {
					continue
				}
				covered = true
				break
			}
		}
		if !covered {
			return "", false, nil
		}
		for _, occ := range part.Occurrences {
			fmt.Fprintf(&out, "source_id %d · %s · 片段 %d · 字节 %d–%d\n", occ.SourceID, part.Direction, occ.Ordinal, part.Start, part.End)
		}
	}
	return out.String(), len(batch.Parts) > 0, nil
}
