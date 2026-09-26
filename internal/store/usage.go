package store

import (
	"context"
	"time"
)

// UsagePoint is a per-day usage bucket.
type UsagePoint struct {
	Day          time.Time `json:"day"`
	Requests     int64     `json:"requests"`
	TokensInput  int64     `json:"tokens_input"`
	TokensOutput int64     `json:"tokens_output"`
}

// UsageBreakdown is a per-key (model or client) aggregate.
type UsageBreakdown struct {
	Key         string `json:"key"`
	Requests    int64  `json:"requests"`
	TokensTotal int64  `json:"tokens_total"`
}

// UsageSummary aggregates request volume and tokens.
type UsageSummary struct {
	Requests        int64            `json:"requests"`
	TokensInput     int64            `json:"tokens_input"`
	TokensCached    int64            `json:"tokens_cached"`
	TokensOutput    int64            `json:"tokens_output"`
	TokensReasoning int64            `json:"tokens_reasoning"`
	ByModel         []UsageBreakdown `json:"by_model"`
	ByClient        []UsageBreakdown `json:"by_client"`
	Daily           []UsagePoint     `json:"daily"`
}

// Usage returns aggregate usage over [from,to). If employeeIDs is non-nil the
// results are restricted to those employees; nil means all employees.
func (s *Store) Usage(ctx context.Context, from, to time.Time, employeeIDs []int64) (UsageSummary, error) {
	var u UsageSummary
	ids := nullableIDs(employeeIDs)

	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(tokens_input),0), COALESCE(SUM(tokens_cached),0),
			COALESCE(SUM(tokens_output),0), COALESCE(SUM(tokens_reasoning),0)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		  AND ($3::bigint[] IS NULL OR employee_id = ANY($3))`,
		from, to, ids).
		Scan(&u.Requests, &u.TokensInput, &u.TokensCached, &u.TokensOutput, &u.TokensReasoning)
	if err != nil {
		return u, err
	}

	u.ByModel, err = s.usageBreakdown(ctx, "model_requested", from, to, ids)
	if err != nil {
		return u, err
	}
	u.ByClient, err = s.usageBreakdown(ctx, "client_kind", from, to, ids)
	if err != nil {
		return u, err
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT date_trunc('day', started_at) AS d, COUNT(*),
			COALESCE(SUM(tokens_input),0), COALESCE(SUM(tokens_output),0)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		  AND ($3::bigint[] IS NULL OR employee_id = ANY($3))
		GROUP BY d ORDER BY d`, from, to, ids)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.Day, &p.Requests, &p.TokensInput, &p.TokensOutput); err != nil {
			return u, err
		}
		u.Daily = append(u.Daily, p)
	}
	return u, rows.Err()
}

func (s *Store) usageBreakdown(ctx context.Context, col string, from, to time.Time, ids []int64) ([]UsageBreakdown, error) {
	// col is a fixed internal identifier, never user input.
	sql := `
		SELECT COALESCE(NULLIF(` + col + `,''),'(none)'), COUNT(*),
			COALESCE(SUM(tokens_input+tokens_output),0)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		  AND ($3::bigint[] IS NULL OR employee_id = ANY($3))
		GROUP BY 1 ORDER BY 2 DESC`
	rows, err := s.Pool.Query(ctx, sql, from, to, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageBreakdown
	for rows.Next() {
		var b UsageBreakdown
		if err := rows.Scan(&b.Key, &b.Requests, &b.TokensTotal); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ActiveEmployeeCount returns distinct employees with a request in [from,to).
func (s *Store) ActiveEmployeeCount(ctx context.Context, from, to time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT employee_id) FROM requests WHERE started_at >= $1 AND started_at < $2`,
		from, to).Scan(&n)
	return n, err
}
