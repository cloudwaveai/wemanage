package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminBriefingPref is one super admin's weekly-briefing preference: whether
// the Friday briefing is pushed to them, and the custom prompt (focus) the
// LLM writes under. An empty prompt means the built-in default.
type AdminBriefingPref struct {
	Enabled   bool
	Prompt    string
	UpdatedAt time.Time
}

// AdminBriefing covers [WeekStart, PeriodEnd). WeekStart retains its legacy
// name in storage; a manually selected range may start on any weekday.
type AdminBriefing struct {
	EmployeeID  int64      `json:"employee_id"`
	WeekStart   time.Time  `json:"week_start"`
	PeriodEnd   time.Time  `json:"period_end"`
	ContentMD   string     `json:"content_md"`
	Model       string     `json:"model"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	GeneratedAt *time.Time `json:"generated_at"`
	SentAt      *time.Time `json:"sent_at"`
}

// GetAdminBriefingPref returns the admin's preference; found is false when
// they never saved one (the defaults then apply: enabled, default prompt).
func (s *Store) GetAdminBriefingPref(ctx context.Context, employeeID int64) (pref AdminBriefingPref, found bool, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT enabled, prompt, updated_at FROM admin_briefing_prefs WHERE employee_id=$1`, employeeID).
		Scan(&pref.Enabled, &pref.Prompt, &pref.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBriefingPref{Enabled: true}, false, nil
	}
	return pref, err == nil, err
}

// PutAdminBriefingPref saves (or replaces) the admin's preference.
func (s *Store) PutAdminBriefingPref(ctx context.Context, employeeID int64, enabled bool, prompt string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO admin_briefing_prefs (employee_id, enabled, prompt) VALUES ($1,$2,$3)
		ON CONFLICT (employee_id) DO UPDATE SET enabled=EXCLUDED.enabled,
			prompt=EXCLUDED.prompt, updated_at=now()`,
		employeeID, enabled, prompt)
	return err
}

// ListSuperAdmins returns every active super admin. Briefing recipients are
// chosen from this list; they may lack a Feishu binding (no push then).
func (s *Store) ListSuperAdmins(ctx context.Context) ([]Employee, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+employeeCols+` FROM employees
		WHERE is_super_admin AND status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// StartAdminBriefing records the running state before the background work starts.
func (s *Store) StartAdminBriefing(ctx context.Context, employeeID int64, from, to time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO admin_briefings (employee_id, week_start, period_end, status) VALUES ($1,$2,$3,'running')
		ON CONFLICT (employee_id, week_start, period_end) DO UPDATE SET
			status='running', error='', content_md='', model='', generated_at=NULL, sent_at=NULL`, employeeID, from, to)
	return err
}

// FinishAdminBriefing records how the run ended. An empty content keeps the
// previous one (there is none for a failed first run, and this mirrors the
// work-digest finish semantics).
func (s *Store) FinishAdminBriefing(ctx context.Context, employeeID int64, from, to time.Time,
	content, model, status, errMsg string, sent bool) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE admin_briefings SET
			content_md = CASE WHEN $4 <> '' THEN $4 ELSE content_md END,
			model = CASE WHEN $4 <> '' THEN $5 ELSE model END,
			status=$6, error=$7, generated_at=now(),
			sent_at = CASE WHEN $8 THEN now() ELSE sent_at END
		WHERE employee_id=$1 AND week_start=$2 AND period_end=$3`,
		employeeID, from, to, content, model, status, errMsg, sent)
	return err
}

// GetAdminBriefing returns an admin's briefing for the week, or ErrNotFound.
func (s *Store) GetAdminBriefing(ctx context.Context, employeeID int64, from, to time.Time) (AdminBriefing, error) {
	var b AdminBriefing
	err := s.Pool.QueryRow(ctx, `
		SELECT employee_id, week_start, period_end, content_md, model, status, error, created_at, generated_at, sent_at
		FROM admin_briefings WHERE employee_id=$1 AND week_start=$2 AND period_end=$3`, employeeID, from, to).
		Scan(&b.EmployeeID, &b.WeekStart, &b.PeriodEnd, &b.ContentMD, &b.Model, &b.Status, &b.Error, &b.CreatedAt, &b.GeneratedAt, &b.SentAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// RecentAdminBriefings lists an admin's briefing runs, newest week first.
func (s *Store) RecentAdminBriefings(ctx context.Context, employeeID int64, limit int) ([]AdminBriefing, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT employee_id, week_start, period_end, content_md, model, status, error, created_at, generated_at, sent_at
		FROM admin_briefings WHERE employee_id=$1 ORDER BY COALESCE(generated_at, created_at) DESC, week_start DESC, period_end DESC LIMIT $2`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminBriefing
	for rows.Next() {
		var b AdminBriefing
		if err := rows.Scan(&b.EmployeeID, &b.WeekStart, &b.PeriodEnd, &b.ContentMD, &b.Model, &b.Status, &b.Error,
			&b.CreatedAt, &b.GeneratedAt, &b.SentAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteExpiredAdminBriefings drops briefings of weeks before cutoff; the
// content is derived from the daily summaries and kept no longer than they
// are. Preferences are tiny and kept.
func (s *Store) DeleteExpiredAdminBriefings(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM admin_briefings WHERE week_start < $1::date`, before)
	return err
}

// WorkDigestDaysInRange returns the summary runs for days in [from,to),
// oldest first. Material for the weekly briefing is assembled from these.
func (s *Store) WorkDigestDaysInRange(ctx context.Context, from, to time.Time) ([]WorkDigestDay, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT day, CASE WHEN privacy_safe OR NOT EXISTS (SELECT 1 FROM employees WHERE private_work_since IS NOT NULL OR name='林伊默') THEN overview_md ELSE '' END, model, status, error, started_at, finished_at
		FROM work_digest_days WHERE day >= $1::date AND day < $2::date ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkDigestDay
	for rows.Next() {
		var d WorkDigestDay
		if err := rows.Scan(&d.Day, &d.OverviewMD, &d.Model, &d.Status, &d.Error, &d.StartedAt, &d.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
