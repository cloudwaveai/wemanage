package workrecords

import (
	"context"
	"crypto/sha256"
	"errors"
)

type ImageAudit struct {
	Fragments           int   `json:"fragments"`
	ReadableImages      int   `json:"readable_images"`
	UnverifiedFragments int   `json:"unverified_fragments"`
	OriginalImageBytes  int64 `json:"original_image_bytes"`
}

// AuditInlineImages reads only metadata about literal image data URLs. It never
// returns an image, stores results or sends anything to a model.
func (q *Queue) AuditInlineImages(ctx context.Context, run int64) (ImageAudit, error) {
	var out ImageAudit
	var private bool
	if err := q.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wm_full_journal_sources s JOIN employees e ON e.id=s.employee_id WHERE s.run_id=r.id AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) FROM wm_full_journal_runs r WHERE r.id=$1`, run).Scan(&private); err != nil {
		return out, err
	}
	if private {
		return out, errors.New("source privacy changed")
	}
	rows, err := q.Store.Pool.Query(ctx, `SELECT hash,data FROM wm_full_journal_blobs WHERE run_id=$1 AND position(convert_to('data:image/','UTF8') in data)>0`, run)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h, data []byte
		if err = rows.Scan(&h, &data); err != nil {
			return out, err
		}
		sum := sha256.Sum256(data)
		if len(h) != len(sum) || string(h) != string(sum[:]) {
			return out, errors.New("snapshot data mismatch")
		}
		out.Fragments++
		refs, e := ScanInlineImages(data)
		if e != nil || len(refs) == 0 {
			out.UnverifiedFragments++
			continue
		}
		out.ReadableImages += len(refs)
		for _, ref := range refs {
			out.OriginalImageBytes += int64(ref.Bytes)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}
