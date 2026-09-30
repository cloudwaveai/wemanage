package store

import (
	"context"
	"time"
)

// QuotaAlertTarget is an employee who wants quota reminders, with their
// account and the windows already reminded about (the reset time of each).
type QuotaAlertTarget struct {
	EmployeeID         int64
	OpenID             string
	AccountID          int64
	AlertedPrimary     *time.Time // 80% reminder sent for the 5-hour window resetting then
	AlertedSecondary   *time.Time // … for the weekly window
	ExhaustedPrimary   *time.Time // "used up" message sent for the 5-hour window
	ExhaustedSecondary *time.Time // … for the weekly window
}

// QuotaAlertTargets lists active employees with the reminder on and an account.
func (s *Store) QuotaAlertTargets(ctx context.Context) ([]QuotaAlertTarget, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id, COALESCE(e.feishu_open_id, ''), a.id, e.quota_alerted_primary, e.quota_alerted_secondary,
			e.quota_exhausted_primary, e.quota_exhausted_secondary
		FROM employees e JOIN upstream_accounts a ON a.employee_id = e.id
		WHERE e.quota_alert AND e.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaAlertTarget
	for rows.Next() {
		var t QuotaAlertTarget
		if err := rows.Scan(&t.EmployeeID, &t.OpenID, &t.AccountID, &t.AlertedPrimary, &t.AlertedSecondary,
			&t.ExhaustedPrimary, &t.ExhaustedSecondary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkQuotaAlerted records that the employee was told about the window
// ("primary" = 5 hours, "secondary" = weekly) resetting at resetAt: the 80%
// reminder, or with exhausted the "used up" message.
func (s *Store) MarkQuotaAlerted(ctx context.Context, employeeID int64, window string, exhausted bool, resetAt time.Time) error {
	col := map[[2]bool]string{
		{false, false}: "quota_alerted_primary", {true, false}: "quota_alerted_secondary",
		{false, true}: "quota_exhausted_primary", {true, true}: "quota_exhausted_secondary",
	}[[2]bool{window == "secondary", exhausted}]
	_, err := s.Pool.Exec(ctx, `UPDATE employees SET `+col+`=$2 WHERE id=$1`, employeeID, resetAt)
	return err
}
