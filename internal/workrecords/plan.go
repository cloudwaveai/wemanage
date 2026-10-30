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
	SourceID       int64     `json:"source_id"`
	EmployeeID     int64     `json:"employee_id"`
	EmployeeName   string    `json:"employee_name,omitempty"`
	SessionID      *int64    `json:"session_id"`
	Time           time.Time `json:"time"`
	Direction      string    `json:"direction"`
	Kind           string    `json:"kind,omitempty"`
	Ordinal        int       `json:"ordinal"`
	Hash           string    `json:"hash"`
	Start          int       `json:"start"`
	End            int       `json:"end"`
	Total          int       `json:"total"`
	Text           string    `json:"text"`
	ReusedFrom     int64     `json:"reused_from_source,omitempty"`
	ReusedSegments int       `json:"reused_request_segments,omitempty"`
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
	prefix := p.SharedPrefix(record)
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
	if prefix > 0 {
		marker := PlannedPart{SourceID: record.SourceID, EmployeeID: record.EmployeeID, EmployeeName: record.EmployeeName, SessionID: record.SessionID, Time: record.StartedAt, Direction: "request", ReusedFrom: prev.source, ReusedSegments: prefix}
		if err := p.appendPart(marker); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		direction string
		segments  []Segment
		start     int
	}{{"request", record.Record.Request, prefix}, {"response", record.Record.Response, 0}} {
		for ordinal := group.start; ordinal < len(group.segments); ordinal++ {
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

// SharedPrefix lets a streaming reader avoid fetching bytes already emitted in
// an earlier request. Add must still receive the entire ordered identity list.
func (p *Planner) SharedPrefix(record SourceRecord) int {
	if record.SessionID == nil {
		return 0
	}
	prev := p.previous[sessionKey{record.EmployeeID, *record.SessionID}]
	i := 0
	for i < len(prev.request) && i < len(record.Record.Request) && prev.request[i].hash == record.Record.Request[i].Hash && prev.request[i].kind == record.Record.Request[i].Kind {
		i++
	}
	return i
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
