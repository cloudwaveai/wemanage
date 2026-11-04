package workrecords

import (
	"encoding/json"
	"errors"
	"time"
)

// compactEvidence is a dry-run format: source timestamps occur once per
// batch, while each occurrence retains its source ID and original ordinal.
// It is not currently accepted by the production model worker.
const compactEvidenceFormat = "session-evidence-v2-dry-run"

type compactSource struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
}
type compactPart struct {
	EmployeeID   int64      `json:"employee_id"`
	EmployeeName string     `json:"employee_name,omitempty"`
	SessionID    *int64     `json:"session_id"`
	Direction    string     `json:"direction"`
	Kind         string     `json:"kind"`
	Hash         string     `json:"hash"`
	Start        int        `json:"start"`
	End          int        `json:"end"`
	Total        int        `json:"total"`
	Opaque       bool       `json:"opaque_encrypted,omitempty"`
	Occurrences  [][2]int64 `json:"occurrences"`
	Text         string     `json:"text,omitempty"`
}
type compactBatch struct {
	Format  string          `json:"format"`
	Sources []compactSource `json:"sources"`
	Parts   []compactPart   `json:"parts"`
}

func compactEvidence(parts []EvidencePart, includeText bool) (compactBatch, error) {
	result := compactBatch{Format: compactEvidenceFormat, Sources: []compactSource{}, Parts: []compactPart{}}
	seen := map[int64]time.Time{}
	for _, part := range parts {
		out := compactPart{EmployeeID: part.EmployeeID, EmployeeName: part.EmployeeName, SessionID: part.SessionID, Direction: part.Direction, Kind: part.Kind, Hash: part.Hash, Start: part.Start, End: part.End, Total: part.Total, Opaque: part.Opaque, Occurrences: make([][2]int64, 0, len(part.Occurrences))}
		if includeText {
			out.Text = part.Text
		}
		for _, o := range part.Occurrences {
			if at, ok := seen[o.SourceID]; ok {
				if !at.Equal(o.Time) {
					return result, errors.New("inconsistent source time")
				}
			} else {
				seen[o.SourceID] = o.Time
				result.Sources = append(result.Sources, compactSource{ID: o.SourceID, Time: o.Time})
			}
			out.Occurrences = append(out.Occurrences, [2]int64{o.SourceID, int64(o.Ordinal)})
		}
		result.Parts = append(result.Parts, out)
	}
	return result, nil
}

type compactPlanner struct {
	maxBytes      int
	parts         []EvidencePart
	batches       int64
	manifestBytes int64
	payloadBytes  int64
}

func (p *compactPlanner) flush() error {
	if len(p.parts) == 0 {
		return nil
	}
	payload, err := compactEvidence(p.parts, true)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	manifest, err := compactEvidence(p.parts, false)
	if err != nil {
		return err
	}
	saved, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	p.batches++
	p.payloadBytes += int64(len(raw))
	p.manifestBytes += int64(len(saved))
	p.parts = nil
	return nil
}
func (p *compactPlanner) add(part EvidencePart) error {
	trial := append(append([]EvidencePart(nil), p.parts...), part)
	batch, err := compactEvidence(trial, true)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	if len(raw) > p.maxBytes && len(p.parts) > 0 {
		if err = p.flush(); err != nil {
			return err
		}
		batch, err = compactEvidence([]EvidencePart{part}, true)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(batch)
		if err != nil {
			return err
		}
	}
	if len(raw) > p.maxBytes {
		return errors.New("compact part exceeds capacity")
	}
	p.parts = append(p.parts, part)
	return nil
}
