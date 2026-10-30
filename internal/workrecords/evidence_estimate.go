package workrecords

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type EvidenceEstimate struct {
	Groups            int64 `json:"groups"`
	Occurrences       int64 `json:"occurrences"`
	Batches           int64 `json:"batches"`
	ManifestBytes     int64 `json:"manifest_bytes"`
	ModelPayloadBytes int64 `json:"model_payload_bytes"`
	MissingSources    int64 `json:"missing_sources"`
}
type evidenceKey struct {
	employee, scope       int64
	name, direction, kind string
	session               int64
	hasSession            bool
	hash                  [32]byte
}

// EstimateEvidence reads the immutable snapshot and computes exact JSON batch
// sizes using the candidate planner. It never writes or invokes a model.
func (q *Queue) EstimateEvidence(ctx context.Context, run int64, progress func(int64)) (EvidenceEstimate, error) {
	var out EvidenceEstimate
	var private bool
	err := q.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=$1 AND (e.private_work_since IS NOT NULL OR e.name='林伊默')),(SELECT count(*) FROM wm_full_journal_sources WHERE run_id=$1 AND status<>'ready')`, run).Scan(&private, &out.MissingSources)
	if err != nil {
		return out, err
	}
	if private {
		return out, errors.New("source privacy changed")
	}
	planner, err := NewEvidencePlanner(100000, func(b EvidenceBatch) error {
		raw, e := json.Marshal(b)
		if e != nil {
			return e
		}
		out.ManifestBytes += int64(len(raw))
		return nil
	})
	if err != nil {
		return out, err
	}
	if err = q.walkEvidence(ctx, run, func(group EvidenceGroup) error {
		out.Groups++
		out.Occurrences += int64(len(group.Occurrences))
		return planner.Add(group)
	}, progress); err != nil {
		return out, err
	}
	if err = planner.Finish(); err != nil {
		return out, err
	}
	out.Batches = planner.batchCount
	out.ModelPayloadBytes = planner.payloadBytes
	return out, nil
}

// walkEvidence uses a deterministic ordering over the immutable snapshot.
func (q *Queue) walkEvidence(ctx context.Context, run int64, consume func(EvidenceGroup) error, progress func(int64)) error {
	var groups int64
	rows, err := q.Store.Pool.Query(ctx, `WITH positions AS (SELECT s.employee_id,s.employee_name,s.session_id,CASE WHEN s.session_id IS NULL THEN s.id ELSE 0 END scope,s.id,s.started_at,g.direction,g.kind,g.hash,g.ordinal,min(s.started_at) OVER w first_time,min(s.id) OVER w first_source FROM wm_full_journal_sources s JOIN wm_full_journal_segments g ON g.source_id=s.id WHERE s.run_id=$1 AND s.status='ready' WINDOW w AS (PARTITION BY s.employee_id,s.employee_name,s.session_id,CASE WHEN s.session_id IS NULL THEN s.id ELSE 0 END,g.direction,g.kind,g.hash)) SELECT employee_id,employee_name,session_id,scope,id,started_at,direction,kind,hash,ordinal FROM positions ORDER BY employee_id,employee_name,session_id NULLS LAST,scope,first_time,first_source,direction,kind,hash,started_at,id,ordinal`, run)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current evidenceKey
	var group EvidenceGroup
	have := false
	flush := func() error {
		if !have {
			return nil
		}
		var data []byte
		if e := q.Store.Pool.QueryRow(ctx, `SELECT data FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=$2`, run, current.hash[:]).Scan(&data); e != nil {
			return e
		}
		group.Segment = Segment{Hash: current.hash, Kind: current.kind, Data: data}
		if e := consume(group); e != nil {
			return fmt.Errorf("invalid evidence snapshot: %w", e)
		}
		groups++
		if progress != nil && groups%1000 == 0 {
			progress(groups)
		}
		return nil
	}
	for rows.Next() {
		var key evidenceKey
		var session *int64
		var source int64
		var at time.Time
		var hash []byte
		var ordinal int
		if err = rows.Scan(&key.employee, &key.name, &session, &key.scope, &source, &at, &key.direction, &key.kind, &hash, &ordinal); err != nil {
			return err
		}
		if len(hash) != 32 {
			return errors.New("invalid evidence hash")
		}
		copy(key.hash[:], hash)
		if session != nil {
			key.hasSession = true
			key.session = *session
		}
		if !have || key != current {
			if err = flush(); err != nil {
				return err
			}
			current = key
			have = true
			group = EvidenceGroup{EmployeeID: key.employee, EmployeeName: key.name, SessionID: session, Direction: key.direction}
		}
		group.Occurrences = append(group.Occurrences, EvidenceOccurrence{SourceID: source, Ordinal: ordinal, Time: at})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if err = flush(); err != nil {
		return err
	}

	return nil
}
