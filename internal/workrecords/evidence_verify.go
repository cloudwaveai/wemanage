package workrecords

import (
	"context"
	"encoding/json"
	"errors"
)

type EvidenceVerification struct {
	Ordinal      int `json:"ordinal"`
	Parts        int `json:"parts"`
	Occurrences  int `json:"occurrences"`
	PayloadBytes int `json:"payload_bytes"`
}

// VerifyEvidenceBatch checks one queued reference batch without a model call
// or persistent writes. It uses the same privacy locks and materialization as
// the model worker and reports only aggregate counts and size.
func (q *Queue) VerifyEvidenceBatch(ctx context.Context, run int64, ordinal int) (EvidenceVerification, error) {
	out := EvidenceVerification{Ordinal: ordinal}
	tx, err := q.Store.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT e.private_work_since IS NULL AND e.name<>'林伊默' FROM employees e WHERE e.id IN (SELECT employee_id FROM wm_full_journal_sources WHERE run_id=$1) ORDER BY e.id FOR SHARE`, run)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ok bool
		if err = rows.Scan(&ok); err != nil {
			rows.Close()
			return out, err
		}
		if !ok {
			rows.Close()
			return out, errors.New("source privacy changed")
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT b.input_manifest FROM wm_full_journal_batches b JOIN wm_full_journal_runs r ON r.id=b.run_id WHERE b.run_id=$1 AND b.phase='extract' AND b.level=0 AND b.ordinal=$2 AND NOT r.superseded AND r.plan_format=$3`, run, ordinal, evidenceFormat).Scan(&raw); err != nil {
		return out, err
	}
	payload, err := materializeEvidence(ctx, tx, run, raw)
	if err != nil {
		return out, err
	}
	var batch EvidenceBatch
	if err = json.Unmarshal(payload, &batch); err != nil {
		return out, err
	}
	out.Parts = len(batch.Parts)
	out.PayloadBytes = len(payload)
	for _, part := range batch.Parts {
		out.Occurrences += len(part.Occurrences)
	}
	return out, nil
}
