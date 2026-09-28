package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Report is an employee's weekly AI-use recap. Date is the week's Monday
// (the table is still named daily_reports).
type Report struct {
	ID          int64
	EmployeeID  int64
	Date        time.Time
	Stats       json.RawMessage
	SummaryMD   string
	Model       string
	GeneratedAt time.Time
	PushedAt    *time.Time
}

// UpsertReport inserts or replaces an employee's report for a week.
func (s *Store) UpsertReport(ctx context.Context, employeeID int64, date time.Time, stats json.RawMessage, summaryMD, model string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO daily_reports (employee_id, date, stats, summary_md, model)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (employee_id, date) DO UPDATE SET
			stats=EXCLUDED.stats, summary_md=EXCLUDED.summary_md, model=EXCLUDED.model,
			generated_at=now(), pushed_at=NULL
		RETURNING id`,
		employeeID, date, []byte(stats), summaryMD, model).Scan(&id)
	return id, err
}

// MarkReportPushed stamps pushed_at.
func (s *Store) MarkReportPushed(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE daily_reports SET pushed_at=now() WHERE id=$1`, id)
	return err
}

// GetReports returns an employee's most recent reports.
func (s *Store) GetReports(ctx context.Context, employeeID int64, limit int) ([]Report, error) {
	if limit <= 0 || limit > 365 {
		limit = 30
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, employee_id, date, stats, summary_md, model, generated_at, pushed_at
		FROM daily_reports WHERE employee_id=$1 ORDER BY date DESC LIMIT $2`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReports(rows)
}

// GetReport returns one report for an employee and week start.
func (s *Store) GetReport(ctx context.Context, employeeID int64, date time.Time) (Report, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, employee_id, date, stats, summary_md, model, generated_at, pushed_at
		FROM daily_reports WHERE employee_id=$1 AND date=$2`, employeeID, date)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()
	list, err := scanReports(rows)
	if err != nil {
		return Report{}, err
	}
	if len(list) == 0 {
		return Report{}, ErrNotFound
	}
	return list[0], nil
}

func scanReports(rows pgx.Rows) ([]Report, error) {
	var out []Report
	for rows.Next() {
		var d Report
		var stats []byte
		if err := rows.Scan(&d.ID, &d.EmployeeID, &d.Date, &stats, &d.SummaryMD, &d.Model, &d.GeneratedAt, &d.PushedAt); err != nil {
			return nil, err
		}
		d.Stats = stats
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReportRecipients returns active employees who used the gateway in [from,to)
// and have not turned their weekly recap off.
func (s *Store) ReportRecipients(ctx context.Context, from, to time.Time) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id FROM employees e
		WHERE e.status = 'active' AND NOT e.weekly_report_off
		  AND EXISTS (SELECT 1 FROM requests r WHERE r.employee_id = e.id AND r.started_at >= $1 AND r.started_at < $2)
		ORDER BY e.id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
