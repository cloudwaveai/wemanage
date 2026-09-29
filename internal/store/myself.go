package store

import (
	"context"
	"time"
)

// QuotaAlertTarget is an employee who wants quota reminders, with their
// account and the windows already reminded about.
type QuotaAlertTarget struct {
	EmployeeID       int64
	OpenID           string
	AccountID        int64
	AlertedPrimary   *time.Time
	AlertedSecondary *time.Time
}

// QuotaAlertTargets lists active employees with the reminder on and an account.
func (s *Store) QuotaAlertTargets(ctx context.Context) ([]QuotaAlertTarget, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id, COALESCE(e.feishu_open_id, ''), a.id, e.quota_alerted_primary, e.quota_alerted_secondary
		FROM employees e JOIN upstream_accounts a ON a.employee_id = e.id
		WHERE e.quota_alert AND e.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaAlertTarget
	for rows.Next() {
		var t QuotaAlertTarget
		if err := rows.Scan(&t.EmployeeID, &t.OpenID, &t.AccountID, &t.AlertedPrimary, &t.AlertedSecondary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkQuotaAlerted records that the employee was reminded about the window
// ("primary" = 5 hours, "secondary" = weekly) resetting at resetAt.
func (s *Store) MarkQuotaAlerted(ctx context.Context, employeeID int64, window string, resetAt time.Time) error {
	col := "quota_alerted_primary"
	if window == "secondary" {
		col = "quota_alerted_secondary"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET `+col+`=$2 WHERE id=$1`, employeeID, resetAt)
	return err
}
