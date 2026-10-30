package workrecords

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SourceRecord is an immutable, already permission-checked snapshot.
type SourceRecord struct {
	SourceID     int64
	EmployeeID   int64
	EmployeeName string
	SessionID    *int64
	StartedAt    time.Time
	Record       Record
}
type PlannedPart struct {
	SourceID            int64     `json:"source_id"`
	EmployeeID          int64     `json:"employee_id"`
	EmployeeName        string    `json:"employee_name,omitempty"`
	SessionID           *int64    `json:"session_id"`
	Time                time.Time `json:"time"`
	Direction           string    `json:"direction"`
	Kind                string    `json:"kind,omitempty"`
	Ordinal             int       `json:"ordinal"`
	Hash                string    `json:"hash"`
	Start               int       `json:"start"`
	End                 int       `json:"end"`
	Total               int       `json:"total"`
	Text                string    `json:"text"`
	ReusedFrom          int64     `json:"reused_from_source,omitempty"`
	ReusedSegments      int       `json:"reused_request_segments,omitempty"`
	ReusedStart         int       `json:"reused_start,omitempty"`
	ReusedPreviousStart int       `json:"reused_previous_start,omitempty"`
}
type PlannedBatch struct {
	Parts []PlannedPart `json:"parts"`
}

// Planner streams bounded batches to emit; only the preceding request hashes
// per session and the current batch stay in memory. Call Add in chronological
// order, then Finish. If emit fails the caller must discard the partial plan.
type sessionKey struct{ employee, session int64 }
type segmentIdentity struct {
	hash [32]byte
	kind string
}
type prior struct {
	source  int64
	request []segmentIdentity
}
type Planner struct {
	maxBytes int
	previous map[sessionKey]prior
	current  PlannedBatch
	emit     func(PlannedBatch) error
}

func NewPlanner(maxBytes int, emit func(PlannedBatch) error) (*Planner, error) {
	if maxBytes < 4096 || emit == nil {
		return nil, errors.New("invalid batch capacity or emitter")
	}
	return &Planner{maxBytes: maxBytes, previous: map[sessionKey]prior{}, current: PlannedBatch{Parts: []PlannedPart{}}, emit: emit}, nil
}
func (p *Planner) appendPart(part PlannedPart) error {
	trial := PlannedBatch{Parts: append(append([]PlannedPart(nil), p.current.Parts...), part)}
	data, err := json.Marshal(trial)
	if err != nil {
		return err
	}
	if len(data) > p.maxBytes && len(p.current.Parts) > 0 {
		if err = p.emit(p.current); err != nil {
			return err
		}
		p.current = PlannedBatch{Parts: []PlannedPart{}}
		data, err = json.Marshal(PlannedBatch{Parts: []PlannedPart{part}})
		if err != nil {
			return err
		}
	}
	if len(data) > p.maxBytes {
		return errors.New("part exceeds batch capacity")
	}
	p.current.Parts = append(p.current.Parts, part)
	return nil
}
func (p *Planner) Add(record SourceRecord) error {
	reuse := p.SharedInput(record)
	var prev prior
	if record.SessionID != nil {
		key := sessionKey{record.EmployeeID, *record.SessionID}
		prev = p.previous[key]
		identity := make([]segmentIdentity, len(record.Record.Request))
		for i, s := range record.Record.Request {
			identity[i] = segmentIdentity{hash: s.Hash, kind: s.Kind}
		}
		p.previous[key] = prior{source: record.SourceID, request: identity}
	}
	if reuse.count > 0 {
		marker := PlannedPart{SourceID: record.SourceID, EmployeeID: record.EmployeeID, EmployeeName: record.EmployeeName, SessionID: record.SessionID, Time: record.StartedAt, Direction: "request", ReusedFrom: prev.source, ReusedSegments: reuse.count, ReusedStart: reuse.current, ReusedPreviousStart: reuse.previous}
		if err := p.appendPart(marker); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		direction string
		segments  []Segment
	}{{"request", record.Record.Request}, {"response", record.Record.Response}} {
		for ordinal := 0; ordinal < len(group.segments); ordinal++ {
			if group.direction == "request" && ordinal >= reuse.current && ordinal < reuse.current+reuse.count {
				continue
			}
			seg := group.segments[ordinal]
			parts, err := Split(seg, (p.maxBytes-2048)/6)
			if err != nil {
				return err
			}
			for _, part := range parts {
				item := PlannedPart{SourceID: record.SourceID, EmployeeID: record.EmployeeID, EmployeeName: record.EmployeeName, SessionID: record.SessionID, Time: record.StartedAt, Direction: group.direction, Kind: seg.Kind, Ordinal: ordinal, Hash: fmt.Sprintf("%x", seg.Hash), Start: part.Start, End: part.End, Total: part.Total, Text: part.Text}
				if err := p.appendPart(item); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// inputReuse identifies only the matching initial input history. Parameter
// segments and every occurrence after the first mismatch remain independent.
type inputReuse struct{ current, previous, count int }

func (p *Planner) SharedInput(record SourceRecord) inputReuse {
	if record.SessionID == nil {
		return inputReuse{}
	}
	prev := p.previous[sessionKey{record.EmployeeID, *record.SessionID}]
	currentStart, previousStart := -1, -1
	for i, s := range record.Record.Request {
		if s.Kind == "input" {
			currentStart = i
			break
		}
	}
	for i, s := range prev.request {
		if s.kind == "input" {
			previousStart = i
			break
		}
	}
	if currentStart < 0 || previousStart < 0 {
		return inputReuse{}
	}
	n := 0
	for currentStart+n < len(record.Record.Request) && previousStart+n < len(prev.request) {
		a, b := record.Record.Request[currentStart+n], prev.request[previousStart+n]
		if a.Kind != "input" || b.kind != "input" || a.Hash != b.hash {
			break
		}
		n++
	}
	return inputReuse{current: currentStart, previous: previousStart, count: n}
}
func (p *Planner) Finish() error {
	if len(p.current.Parts) == 0 {
		return nil
	}
	if err := p.emit(p.current); err != nil {
		return err
	}
	p.current = PlannedBatch{Parts: []PlannedPart{}}
	return nil
}

// Plan is an in-memory convenience wrapper; workers should use Planner.
func Plan(records []SourceRecord, maxBytes int) ([]PlannedBatch, error) {
	var batches []PlannedBatch
	p, err := NewPlanner(maxBytes, func(b PlannedBatch) error { batches = append(batches, b); return nil })
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if err = p.Add(record); err != nil {
			return nil, err
		}
	}
	if err = p.Finish(); err != nil {
		return nil, err
	}
	return batches, nil
}
