package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
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

// RequestBrief is one request as the employee's status page shows it.
type RequestBrief struct {
	At         time.Time
	ClientKind string
	Model      string
	StatusCode int
	ErrorType  string
}

// LastRequests returns the employee's latest successful request and latest
// failed one in the last 30 days (nil when there is none).
func (s *Store) LastRequests(ctx context.Context, employeeID int64) (ok, failed *RequestBrief, err error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (good) (status_code BETWEEN 200 AND 299) AS good,
			started_at, client_kind, model_requested, status_code, error_type
		FROM requests
		WHERE employee_id = $1 AND started_at > now() - interval '30 days'
		ORDER BY good, started_at DESC`, employeeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var good bool
		var b RequestBrief
		if err := rows.Scan(&good, &b.At, &b.ClientKind, &b.Model, &b.StatusCode, &b.ErrorType); err != nil {
			return nil, nil, err
		}
		if good {
			ok = &b
		} else {
			failed = &b
		}
	}
	return ok, failed, rows.Err()
}

// RequestsSince counts the employee's requests since t.
func (s *Store) RequestsSince(ctx context.Context, employeeID int64, t time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests WHERE employee_id=$1 AND started_at >= $2`, employeeID, t).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
