package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// LastSuccess is an employee's latest request through the gateway that
// succeeded, with the client that sent it.
type LastSuccess struct {
	At        time.Time
	UserAgent string
}

// LastSuccess returns the employee's latest successful request, or
// ErrNotFound while none has succeeded yet.
func (s *Store) LastSuccess(ctx context.Context, employeeID int64) (LastSuccess, error) {
	var l LastSuccess
	err := s.Pool.QueryRow(ctx, `
		SELECT started_at, user_agent FROM requests
		WHERE employee_id = $1 AND status_code < 400
		ORDER BY started_at DESC LIMIT 1`, employeeID).Scan(&l.At, &l.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return LastSuccess{}, ErrNotFound
	}
	return l, err
}

// QuotaAlertTarget is one account of an employee who wants quota reminders,
// with the windows already reminded about (the reset time of each).
type QuotaAlertTarget struct {
	EmployeeID         int64
	OpenID             string
	AccountID          int64
	Accounts           int        // how many accounts the employee has
	AlertedPrimary     *time.Time // 80% reminder sent for the 5-hour window resetting then
	AlertedSecondary   *time.Time // … for the weekly window
	ExhaustedPrimary   *time.Time // "used up" message sent for the 5-hour window
	ExhaustedSecondary *time.Time // … for the weekly window
}

// QuotaAlertTargets lists, for active employees with the reminder on, each of
// their accounts.
func (s *Store) QuotaAlertTargets(ctx context.Context) ([]QuotaAlertTarget, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id, COALESCE(e.feishu_open_id, ''), m.account_id,
			COUNT(*) OVER (PARTITION BY e.id),
			m.alerted_primary, m.alerted_secondary, m.exhausted_primary, m.exhausted_secondary
		FROM employees e JOIN account_members m ON m.employee_id = e.id
		WHERE e.quota_alert AND e.status = 'active'
		ORDER BY e.id, m.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuotaAlertTarget
	for rows.Next() {
		var t QuotaAlertTarget
		if err := rows.Scan(&t.EmployeeID, &t.OpenID, &t.AccountID, &t.Accounts, &t.AlertedPrimary, &t.AlertedSecondary,
			&t.ExhaustedPrimary, &t.ExhaustedSecondary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkQuotaAlerted records that the employee was told about the account's
// window ("primary" = 5 hours, "secondary" = weekly) resetting at resetAt:
// the 80% reminder, or with exhausted the "used up" message.
func (s *Store) MarkQuotaAlerted(ctx context.Context, employeeID, accountID int64, window string, exhausted bool, resetAt time.Time) error {
	col := map[[2]bool]string{
		{false, false}: "alerted_primary", {true, false}: "alerted_secondary",
		{false, true}: "exhausted_primary", {true, true}: "exhausted_secondary",
	}[[2]bool{window == "secondary", exhausted}]
	_, err := s.Pool.Exec(ctx, `UPDATE account_members SET `+col+`=$3 WHERE employee_id=$1 AND account_id=$2`,
		employeeID, accountID, resetAt)
	return err
}
