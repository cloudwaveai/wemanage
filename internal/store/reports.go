package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// DailyReport is a generated per-employee daily work summary.
type DailyReport struct {
	ID          int64
	EmployeeID  int64
	Date        time.Time
	Stats       json.RawMessage
	SummaryMD   string
	Model       string
	GeneratedAt time.Time
	PushedAt    *time.Time
}

// UpsertDailyReport inserts or replaces a day's report for an employee.
func (s *Store) UpsertDailyReport(ctx context.Context, employeeID int64, date time.Time, stats json.RawMessage, summaryMD, model string) (int64, error) {
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

// GetDailyReports returns an employee's most recent reports.
func (s *Store) GetDailyReports(ctx context.Context, employeeID int64, limit int) ([]DailyReport, error) {
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

// GetDailyReport returns one report for an employee+date.
func (s *Store) GetDailyReport(ctx context.Context, employeeID int64, date time.Time) (DailyReport, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, employee_id, date, stats, summary_md, model, generated_at, pushed_at
		FROM daily_reports WHERE employee_id=$1 AND date=$2`, employeeID, date)
	if err != nil {
		return DailyReport{}, err
	}
	defer rows.Close()
	list, err := scanReports(rows)
	if err != nil {
		return DailyReport{}, err
	}
	if len(list) == 0 {
		return DailyReport{}, ErrNotFound
	}
	return list[0], nil
}

func scanReports(rows pgx.Rows) ([]DailyReport, error) {
	var out []DailyReport
	for rows.Next() {
		var d DailyReport
		var stats []byte
		if err := rows.Scan(&d.ID, &d.EmployeeID, &d.Date, &stats, &d.SummaryMD, &d.Model, &d.GeneratedAt, &d.PushedAt); err != nil {
			return nil, err
		}
		d.Stats = stats
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveEmployeesOn returns employee ids with at least one request in [from,to).
func (s *Store) ActiveEmployeesOn(ctx context.Context, from, to time.Time) ([]int64, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT DISTINCT employee_id FROM requests WHERE started_at >= $1 AND started_at < $2`, from, to)
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
