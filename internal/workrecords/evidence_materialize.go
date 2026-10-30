package workrecords

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

// Materialize only after privacy rows are locked; the same run's snapshot is
// the only allowed source for text, which is verified again before use.
func materializeEvidence(ctx context.Context, tx pgx.Tx, run int64, manifest []byte) ([]byte, error) {
	var header struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(manifest, &header); err != nil {
		return nil, err
	}
	if header.Format == "" {
		return manifest, nil
	}
	if header.Format != evidenceFormat {
		return nil, errors.New("unknown evidence format")
	}
	var batch EvidenceBatch
	if err := json.Unmarshal(manifest, &batch); err != nil {
		return nil, err
	}
	// Every occurrence must match the original run, owner, session, timestamp,
	// direction, position, kind and digest. Reject corrupted or crossed references
	// before loading text or making any model call.
	var valid bool
	if err := tx.QueryRow(ctx, `WITH refs AS (
       SELECT (p->>'employee_id')::bigint employee_id,p->>'employee_name' employee_name,
              (p->>'session_id')::bigint session_id,p->>'direction' direction,p->>'kind' kind,
              decode(p->>'hash','hex') hash,(o->>'source_id')::bigint source_id,
              (o->>'ordinal')::int ordinal,(o->>'time')::timestamptz started_at
       FROM jsonb_array_elements($2::jsonb->'parts') p
       CROSS JOIN LATERAL jsonb_array_elements(p->'occurrences') o
    ) SELECT NOT EXISTS(SELECT 1 FROM refs x
       LEFT JOIN wm_full_journal_sources s ON s.id=x.source_id AND s.run_id=$1
       LEFT JOIN wm_full_journal_segments g ON g.source_id=s.id AND g.direction=x.direction AND g.ordinal=x.ordinal
       WHERE s.id IS NULL OR s.employee_id IS DISTINCT FROM x.employee_id OR s.employee_name IS DISTINCT FROM COALESCE(x.employee_name,'') OR s.session_id IS DISTINCT FROM x.session_id OR s.started_at IS DISTINCT FROM x.started_at OR g.hash IS DISTINCT FROM x.hash OR g.kind IS DISTINCT FROM x.kind)`, run, manifest).Scan(&valid); err != nil {
		return nil, err
	}
	if !valid {
		return nil, errors.New("evidence occurrence does not match snapshot")
	}
	seen := map[string]bool{}
	hashes := [][]byte{}
	for _, part := range batch.Parts {
		if seen[part.Hash] {
			continue
		}
		h, e := hex.DecodeString(part.Hash)
		if e != nil || len(h) != 32 {
			return nil, errors.New("invalid evidence hash")
		}
		seen[part.Hash] = true
		hashes = append(hashes, h)
	}
	rows, err := tx.Query(ctx, `SELECT hash,data FROM wm_full_journal_blobs WHERE run_id=$1 AND hash=ANY($2)`, run, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blobs := map[string][]byte{}
	for rows.Next() {
		var hash, data []byte
		if err = rows.Scan(&hash, &data); err != nil {
			return nil, err
		}
		blobs[hex.EncodeToString(hash)] = data
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return RestoreEvidence(batch, blobs, 100000)
}
