package store

import (
	"context"
	"encoding/json"
	"time"
)

// Audit appends an immutable audit record.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	d, _ := json.Marshal(detail)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_employee_id, action, target_type, target_id, detail, ip)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		e.ActorID, e.Action, e.TargetType, e.TargetID, d, e.IP)
	return err
}

// AuditFilter narrows ListAudit.
type AuditFilter struct {
	Limit int
}

// AuditRow is a stored audit record with actor name.
type AuditRow struct {
	ID         int64
	ActorID    *int64
	ActorName  string
	Action     string
	TargetType string
	TargetID   string
	Detail     map[string]any
	IP         string
	At         time.Time
}

// ListAudit returns recent audit records, newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT l.id, l.actor_employee_id, COALESCE(e.name,''), l.action, l.target_type, l.target_id, l.detail, l.ip, l.at
		FROM audit_logs l
		LEFT JOIN employees e ON e.id = l.actor_employee_id
		ORDER BY l.at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var d []byte
		if err := rows.Scan(&r.ID, &r.ActorID, &r.ActorName, &r.Action, &r.TargetType, &r.TargetID, &d, &r.IP, &r.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &r.Detail)
		out = append(out, r)
	}
	return out, rows.Err()
}
