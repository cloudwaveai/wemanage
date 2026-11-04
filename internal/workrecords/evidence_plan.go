package workrecords

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

const evidenceFormat = "session-evidence-v1"
const opaquePrototypeFormat = "session-evidence-v2-prototype"

// EvidenceOccurrence preserves each position, including repeated occurrences
// of identical text in a single source. Source timestamps are not inferred.
type EvidenceOccurrence struct {
	SourceID int64     `json:"source_id"`
	Ordinal  int       `json:"ordinal"`
	Time     time.Time `json:"time"`
}

type EvidencePart struct {
	EmployeeID   int64                `json:"employee_id"`
	EmployeeName string               `json:"employee_name,omitempty"`
	SessionID    *int64               `json:"session_id"`
	Direction    string               `json:"direction"`
	Kind         string               `json:"kind"`
	Hash         string               `json:"hash"`
	Start        int                  `json:"start"`
	End          int                  `json:"end"`
	Total        int                  `json:"total"`
	Opaque       bool                 `json:"opaque_encrypted,omitempty"`
	Occurrences  []EvidenceOccurrence `json:"occurrences"`
	// Text is omitted in durable manifests and supplied from the immutable
	// snapshot only when the model is called.
	Text string `json:"text,omitempty"`
}
type EvidenceBatch struct {
	Format string         `json:"format"`
	Parts  []EvidencePart `json:"parts"`
}
type EvidenceGroup struct {
	EmployeeID   int64
	EmployeeName string
	SessionID    *int64
	Direction    string
	Segment      Segment
	Occurrences  []EvidenceOccurrence
}

// EvidencePlanner stores only references. Size checks include hydrated text,
// so a prepared batch cannot exceed the model payload bound on restoration.
// Emit must persist the batch transactionally with its planning checkpoint.
type EvidencePlanner struct {
	maxBytes     int
	format       string
	current      EvidenceBatch
	emit         func(EvidenceBatch) error
	emitHydrated func(EvidenceBatch) error
	payloadBytes int64
	batchCount   int64
}

func NewEvidencePlanner(maxBytes int, emit func(EvidenceBatch) error) (*EvidencePlanner, error) {
	if maxBytes < 4096 || emit == nil {
		return nil, errors.New("invalid evidence capacity or emitter")
	}
	return &EvidencePlanner{maxBytes: maxBytes, format: evidenceFormat, current: EvidenceBatch{Format: evidenceFormat, Parts: []EvidencePart{}}, emit: emit}, nil
}

// enableOpaquePrototype is used only by the offline estimator. It cannot be
// published as a production v1 batch, which has different semantics.
func (p *EvidencePlanner) enableOpaquePrototype() error {
	if len(p.current.Parts) != 0 || p.batchCount != 0 {
		return errors.New("prototype must be selected before planning")
	}
	p.format = opaquePrototypeFormat
	p.current.Format = opaquePrototypeFormat
	return nil
}
func (p *EvidencePlanner) emitCurrent() error {
	if len(p.current.Parts) == 0 {
		return nil
	}
	payload, err := json.Marshal(p.current)
	if err != nil {
		return err
	}
	if p.emitHydrated != nil {
		if err := p.emitHydrated(p.current); err != nil {
			return err
		}
	}
	durable := EvidenceBatch{Format: p.format, Parts: append([]EvidencePart(nil), p.current.Parts...)}
	for i := range durable.Parts {
		durable.Parts[i].Text = ""
	}
	if err := p.emit(durable); err != nil {
		return err
	}
	p.payloadBytes += int64(len(payload))
	p.batchCount++
	p.current = EvidenceBatch{Format: p.format, Parts: []EvidencePart{}}
	return nil
}
func (p *EvidencePlanner) append(part EvidencePart) error {
	trial := EvidenceBatch{Format: p.format, Parts: append(append([]EvidencePart(nil), p.current.Parts...), part)}
	raw, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	if len(raw) > p.maxBytes && len(p.current.Parts) > 0 {
		if err = p.emitCurrent(); err != nil {
			return err
		}
		raw, err = json.Marshal(EvidenceBatch{Format: p.format, Parts: []EvidencePart{part}})
		if err != nil {
			return err
		}
	}
	if len(raw) > p.maxBytes {
		return errors.New("evidence part exceeds capacity")
	}
	p.current.Parts = append(p.current.Parts, part)
	return nil
}
func (p *EvidencePlanner) Add(group EvidenceGroup) error {
	if len(group.Occurrences) == 0 || group.EmployeeID <= 0 || (group.Direction != "request" && group.Direction != "response") {
		return errors.New("invalid evidence group")
	}
	if sha256.Sum256(group.Segment.Data) != group.Segment.Hash {
		return errors.New("evidence hash mismatch")
	}
	// Every source byte remains reconstructable. Only a verified root
	// ciphertext field is represented by its exact range and an explicit
	// marker; its bytes remain unchanged in the immutable source snapshot.
	type fragment struct {
		part   Part
		opaque bool
	}
	var fragments []fragment
	addRange := func(start, end int) error {
		if start == end {
			return nil
		}
		sub := Segment{Hash: group.Segment.Hash, Kind: group.Segment.Kind, Data: group.Segment.Data[start:end]}
		pieces, e := Split(sub, (p.maxBytes-2048)/12)
		if e != nil {
			return e
		}
		for _, piece := range pieces {
			piece.Start += start
			piece.End += start
			piece.Total = len(group.Segment.Data)
			fragments = append(fragments, fragment{part: piece})
		}
		return nil
	}
	var start, end int
	var opaque bool
	var err error
	if p.format == opaquePrototypeFormat {
		start, end, opaque, err = reasoningCipherRange(group.Segment.Data)
		if err != nil {
			return err
		}
	}
	if opaque {
		projected := string(group.Segment.Data[:start]) + strconv.Quote(opaqueReasoningText) + string(group.Segment.Data[end:])
		if len(projected) <= (p.maxBytes-2048)/12 {
			fragments = append(fragments, fragment{part: Part{Start: 0, End: len(group.Segment.Data), Total: len(group.Segment.Data), Text: projected}, opaque: true})
		} else {
			if err = addRange(0, start); err != nil {
				return err
			}
			fragments = append(fragments, fragment{part: Part{Start: start, End: end, Total: len(group.Segment.Data), Text: opaqueReasoningText}, opaque: true})
			if err = addRange(end, len(group.Segment.Data)); err != nil {
				return err
			}
		}
	} else {
		pieces, e := Split(group.Segment, (p.maxBytes-2048)/12)
		if e != nil {
			return e
		}
		for _, piece := range pieces {
			fragments = append(fragments, fragment{part: piece})
		}
	}
	for _, fragment := range fragments {
		piece := fragment.part
		maxRefs := max(1, (p.maxBytes-2048)/256)
		for start := 0; start < len(group.Occurrences); start += maxRefs {
			end := min(start+maxRefs, len(group.Occurrences))
			part := EvidencePart{EmployeeID: group.EmployeeID, EmployeeName: group.EmployeeName, SessionID: group.SessionID, Direction: group.Direction, Kind: group.Segment.Kind, Hash: hex.EncodeToString(group.Segment.Hash[:]), Start: piece.Start, End: piece.End, Total: piece.Total, Opaque: fragment.opaque, Text: piece.Text, Occurrences: append([]EvidenceOccurrence(nil), group.Occurrences[start:end]...)}
			if err = p.append(part); err != nil {
				return err
			}
		}
	}
	return nil
}
func (p *EvidencePlanner) Finish() error { return p.emitCurrent() }

// RestoreEvidence fills all referenced text and rejects malformed or changed
// bytes before the payload reaches a model. The caller supplies only blobs
// belonging to the same immutable run and checks access before fetching.
func RestoreEvidence(batch EvidenceBatch, blobs map[string][]byte, maxBytes int) ([]byte, error) {
	if (batch.Format != evidenceFormat && batch.Format != opaquePrototypeFormat) || len(batch.Parts) == 0 {
		return nil, errors.New("invalid evidence batch")
	}
	for i := range batch.Parts {
		part := &batch.Parts[i]
		data, ok := blobs[part.Hash]
		if !ok {
			return nil, errors.New("missing evidence blob")
		}
		sum := sha256.Sum256(data)
		if fmt.Sprintf("%x", sum) != part.Hash || part.Total != len(data) || part.Start < 0 || part.End < part.Start || part.End > len(data) || len(part.Occurrences) == 0 || !utf8.Valid(data) || !utf8.Valid(data[part.Start:part.End]) {
			return nil, errors.New("invalid evidence reference")
		}
		if batch.Format == evidenceFormat {
			if part.Opaque {
				return nil, errors.New("opaque reference requires prototype format")
			}
			part.Text = string(data[part.Start:part.End])
			continue
		}
		cipherStart, cipherEnd, hasCipher, e := reasoningCipherRange(data)
		if e != nil {
			return nil, e
		}
		overlapsCipher := hasCipher && part.Start < cipherEnd && part.End > cipherStart
		if part.Opaque {
			whole := part.Start == 0 && part.End == len(data)
			onlyCipher := part.Start == cipherStart && part.End == cipherEnd
			if !hasCipher || (!whole && !onlyCipher) {
				return nil, errors.New("invalid opaque evidence range")
			}
			if whole {
				part.Text = string(data[:cipherStart]) + strconv.Quote(opaqueReasoningText) + string(data[cipherEnd:])
			} else {
				part.Text = opaqueReasoningText
			}
		} else {
			if overlapsCipher {
				return nil, errors.New("encrypted evidence requires an opaque reference")
			}
			part.Text = string(data[part.Start:part.End])
		}
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, errors.New("restored evidence exceeds capacity")
	}
	return raw, nil
}
