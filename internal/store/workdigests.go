package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// WorkDigest is one employee's LLM-written summary of a day's work.
type WorkDigest struct {
	Day         time.Time `json:"day"`
	EmployeeID  int64     `json:"employee_id"`
	SummaryMD   string    `json:"summary_md"`
	Sessions    int       `json:"sessions"`
	Messages    int       `json:"messages"`
	Model       string    `json:"model"`
	GeneratedAt time.Time `json:"generated_at"`
}

// WorkDigestDay is a summarized day: the team overview and the run's state.
type WorkDigestDay struct {
	Day        time.Time  `json:"day"`
	OverviewMD string     `json:"overview_md"`
	Model      string     `json:"model"`
	Status     string     `json:"status"` // running | done | failed
	Error      string     `json:"error"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// StartWorkDigestDay marks a day's summary run as started.
func (s *Store) StartWorkDigestDay(ctx context.Context, day time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO work_digest_days (day, status) VALUES ($1, 'running')
		ON CONFLICT (day) DO UPDATE SET status='running', error='', started_at=now(), finished_at=NULL`, day)
	return err
}

// FinishWorkDigestDay records how a day's run ended. An empty overview keeps
// the previous one, so a failed rerun does not wipe a good summary.
func (s *Store) FinishWorkDigestDay(ctx context.Context, day time.Time, overviewMD, model, status, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE work_digest_days SET
			overview_md = CASE WHEN $2 <> '' THEN $2 ELSE overview_md END,
			model = CASE WHEN $2 <> '' THEN $3 ELSE model END,
			status=$4, error=$5, finished_at=now(), privacy_safe=($4='done' AND $2 <> '' AND started_at >= COALESCE((SELECT max(private_work_since) FROM employees), '-infinity'::timestamptz))
		WHERE day=$1`, day, overviewMD, model, status, errMsg)
	return err
}

// GetWorkDigestDay returns a day's run, or ErrNotFound.
func (s *Store) GetWorkDigestDay(ctx context.Context, day time.Time) (WorkDigestDay, error) {
	var d WorkDigestDay
	err := s.Pool.QueryRow(ctx, `
		SELECT day, CASE WHEN privacy_safe OR NOT EXISTS (SELECT 1 FROM employees WHERE private_work_since IS NOT NULL OR name='林伊默') THEN overview_md ELSE '' END, model, status, error, started_at, finished_at
		FROM work_digest_days WHERE day=$1`, day).
		Scan(&d.Day, &d.OverviewMD, &d.Model, &d.Status, &d.Error, &d.StartedAt, &d.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// WorkDigestDays lists the most recent summarized days, newest first.
func (s *Store) WorkDigestDays(ctx context.Context, limit int) ([]time.Time, error) {
	rows, err := s.Pool.Query(ctx, `SELECT day FROM work_digest_days ORDER BY day DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveWorkDigest stores (or replaces) an employee's summary of a day.
func (s *Store) SaveWorkDigest(ctx context.Context, d WorkDigest) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO work_digests (day, employee_id, summary_md, sessions, messages, model)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (day, employee_id) DO UPDATE SET summary_md=EXCLUDED.summary_md,
			sessions=EXCLUDED.sessions, messages=EXCLUDED.messages, model=EXCLUDED.model, generated_at=now()`,
		d.Day, d.EmployeeID, d.SummaryMD, d.Sessions, d.Messages, d.Model)
	return err
}

// PruneWorkDigests drops a day's summaries of employees outside keep (who no
// longer have anything to summarize that day).
func (s *Store) PruneWorkDigests(ctx context.Context, day time.Time, keep []int64) error {
	if keep == nil {
		keep = []int64{}
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM work_digests WHERE day=$1 AND NOT (employee_id = ANY($2)) AND NOT EXISTS (SELECT 1 FROM employees e WHERE e.id=employee_id AND (e.private_work_since IS NOT NULL OR e.name='林伊默'))`, day, keep)
	return err
}

// WorkDigests returns a day's employee summaries.
func (s *Store) WorkDigests(ctx context.Context, day time.Time) ([]WorkDigest, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT day, employee_id, summary_md, sessions, messages, model, generated_at
		FROM work_digests wd WHERE day=$1 AND NOT EXISTS (SELECT 1 FROM employees e WHERE e.id=wd.employee_id AND (e.private_work_since IS NOT NULL OR e.name='林伊默')) ORDER BY employee_id`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkDigest
	for rows.Next() {
		var d WorkDigest
		if err := rows.Scan(&d.Day, &d.EmployeeID, &d.SummaryMD, &d.Sessions, &d.Messages, &d.Model, &d.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteExpiredWorkDigests removes summaries of days before cutoff; they are
// derived from conversation text and kept no longer than it.
func (s *Store) DeleteExpiredWorkDigests(ctx context.Context, before time.Time) error {
	if _, err := s.Pool.Exec(ctx, `DELETE FROM work_digests WHERE day < $1::date`, before); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM work_digest_days WHERE day < $1::date`, before)
	return err
}

// DayText is one stored conversation turn.
type DayText struct {
	SessionID *int64
	StartedAt time.Time
	Role      string
	Text      string
}

// TextEmployees returns the active, non-service employees with conversation
// text in [from,to).
func (s *Store) TextEmployees(ctx context.Context, from, to time.Time) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id FROM employees e
		WHERE e.status='active' AND NOT e.is_service AND e.private_work_since IS NULL AND e.name <> '林伊默'
		  AND EXISTS (SELECT 1 FROM request_texts t WHERE t.employee_id=e.id AND t.started_at >= $1 AND t.started_at < $2)
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

// EmployeeTexts returns an employee's conversation turns in [from,to), by
// session and then time.
func (s *Store) EmployeeTexts(ctx context.Context, employeeID int64, from, to time.Time) ([]DayText, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT session_id, started_at, role, text FROM request_texts
		WHERE employee_id=$1 AND started_at >= $2 AND started_at < $3
		ORDER BY started_at, id`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayText
	for rows.Next() {
		var t DayText
		if err := rows.Scan(&t.SessionID, &t.StartedAt, &t.Role, &t.Text); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EmployeeWork is one employee's gateway use over a period.
type EmployeeWork struct {
	EmployeeID      int64      `json:"employee_id"`
	Requests        int64      `json:"requests"`
	TokensInput     int64      `json:"tokens_input"`
	TokensCached    int64      `json:"tokens_cached"`
	TokensOutput    int64      `json:"tokens_output"`
	TokensReasoning int64      `json:"tokens_reasoning"`
	Sessions        int64      `json:"sessions"`
	ActiveDays      int64      `json:"active_days"`
	LastActive      *time.Time `json:"last_active"`
	TopModel        string     `json:"top_model"`
}

// WorkByEmployee returns every employee's usage in [from,to) (days counted in
// loc's calendar). Employees without requests are omitted.
func (s *Store) WorkByEmployee(ctx context.Context, from, to time.Time, loc *time.Location) (map[int64]EmployeeWork, error) {
	if loc == nil {
		loc = time.UTC
	}
	f := from.In(loc)
	dayOrigin := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc) // days start at loc's midnight
	rows, err := s.Pool.Query(ctx, `
		SELECT employee_id, COUNT(*),
			COALESCE(SUM(tokens_input),0), COALESCE(SUM(tokens_cached),0),
			COALESCE(SUM(tokens_output),0), COALESCE(SUM(tokens_reasoning),0),
			COUNT(DISTINCT session_id), COUNT(DISTINCT date_bin('1 day', started_at, $3)), MAX(started_at)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		GROUP BY employee_id`, from, to, dayOrigin)
	if err != nil {
		return nil, err
	}
	out := map[int64]EmployeeWork{}
	for rows.Next() {
		var w EmployeeWork
		if err := rows.Scan(&w.EmployeeID, &w.Requests, &w.TokensInput, &w.TokensCached, &w.TokensOutput,
			&w.TokensReasoning, &w.Sessions, &w.ActiveDays, &w.LastActive); err != nil {
			rows.Close()
			return nil, err
		}
		out[w.EmployeeID] = w
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Each employee's most-used model, by tokens.
	rows, err = s.Pool.Query(ctx, `
		SELECT DISTINCT ON (employee_id) employee_id, model_requested FROM (
			SELECT employee_id, model_requested, SUM(tokens_input + tokens_output) AS t
			FROM requests
			WHERE started_at >= $1 AND started_at < $2 AND model_requested <> ''
			GROUP BY 1, 2) x
		ORDER BY employee_id, t DESC, model_requested`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var model string
		if err := rows.Scan(&id, &model); err != nil {
			return nil, err
		}
		if w, ok := out[id]; ok {
			w.TopModel = model
			out[id] = w
		}
	}
	return out, rows.Err()
}
