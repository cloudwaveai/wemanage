package store

import (
	"context"
	"fmt"
	"time"
)

// UsagePoint is a per-day usage bucket.
type UsagePoint struct {
	Day          time.Time `json:"day"`
	Requests     int64     `json:"requests"`
	TokensInput  int64     `json:"tokens_input"`
	TokensOutput int64     `json:"tokens_output"`
}

// UsageBreakdown is a per-key (model or client) aggregate. Cached tokens are
// part of input and reasoning tokens part of output, so Total is their sum.
type UsageBreakdown struct {
	Key             string `json:"key"`
	Requests        int64  `json:"requests"`
	TokensTotal     int64  `json:"tokens_total"`
	TokensInput     int64  `json:"tokens_input"`
	TokensCached    int64  `json:"tokens_cached"`
	TokensOutput    int64  `json:"tokens_output"`
	TokensReasoning int64  `json:"tokens_reasoning"`
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
			COALESCE(SUM(tokens_input+tokens_output),0),
			COALESCE(SUM(tokens_input),0), COALESCE(SUM(tokens_cached),0),
			COALESCE(SUM(tokens_output),0), COALESCE(SUM(tokens_reasoning),0)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		  AND ($3::bigint[] IS NULL OR employee_id = ANY($3))
		GROUP BY 1 ORDER BY 3 DESC`
	rows, err := s.Pool.Query(ctx, sql, from, to, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageBreakdown
	for rows.Next() {
		var b UsageBreakdown
		if err := rows.Scan(&b.Key, &b.Requests, &b.TokensTotal,
			&b.TokensInput, &b.TokensCached, &b.TokensOutput, &b.TokensReasoning); err != nil {
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

// MemberUsage is one employee's usage over a period.
type MemberUsage struct {
	EmployeeID   int64      `json:"employee_id"`
	Requests     int64      `json:"requests"`
	TokensInput  int64      `json:"tokens_input"`
	TokensOutput int64      `json:"tokens_output"`
	LastActive   *time.Time `json:"last_active"`
}

// UsageByEmployee returns per-employee usage over [from,to) for the given
// employees (nil = all). Employees without requests are omitted.
func (s *Store) UsageByEmployee(ctx context.Context, from, to time.Time, employeeIDs []int64) (map[int64]MemberUsage, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT employee_id, COUNT(*), COALESCE(SUM(tokens_input),0), COALESCE(SUM(tokens_output),0), MAX(started_at)
		FROM requests
		WHERE started_at >= $1 AND started_at < $2
		  AND ($3::bigint[] IS NULL OR employee_id = ANY($3))
		GROUP BY employee_id`, from, to, nullableIDs(employeeIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]MemberUsage{}
	for rows.Next() {
		var m MemberUsage
		if err := rows.Scan(&m.EmployeeID, &m.Requests, &m.TokensInput, &m.TokensOutput, &m.LastActive); err != nil {
			return nil, err
		}
		out[m.EmployeeID] = m
	}
	return out, rows.Err()
}

// AccountUsage is one upstream account's traffic through the gateway.
type AccountUsage struct {
	AccountID              int64
	Requests24h, Tokens24h int64
	Requests7d, Tokens7d   int64
	RequestsAll, TokensAll int64
	LastActive             *time.Time
}

// UsageByAccount returns every account's requests and tokens (input+output)
// over the last 24 hours, the last 7 days and all time. Accounts without
// requests are omitted.
func (s *Store) UsageByAccount(ctx context.Context) (map[int64]AccountUsage, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT account_id,
			COUNT(*) FILTER (WHERE started_at >= now() - interval '24 hours'),
			COALESCE(SUM(tokens_input + tokens_output) FILTER (WHERE started_at >= now() - interval '24 hours'), 0),
			COUNT(*) FILTER (WHERE started_at >= now() - interval '7 days'),
			COALESCE(SUM(tokens_input + tokens_output) FILTER (WHERE started_at >= now() - interval '7 days'), 0),
			COUNT(*), COALESCE(SUM(tokens_input + tokens_output), 0), MAX(started_at)
		FROM requests WHERE account_id IS NOT NULL
		GROUP BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]AccountUsage{}
	for rows.Next() {
		var u AccountUsage
		if err := rows.Scan(&u.AccountID, &u.Requests24h, &u.Tokens24h, &u.Requests7d, &u.Tokens7d,
			&u.RequestsAll, &u.TokensAll, &u.LastActive); err != nil {
			return nil, err
		}
		out[u.AccountID] = u
	}
	return out, rows.Err()
}

// SeriesPoint is one time bucket of usage.
type SeriesPoint struct {
	T        time.Time `json:"t"`
	Requests int64     `json:"requests"`
	Tokens   int64     `json:"tokens"` // input + output
}

// FirstRequestAt returns the earliest request in [from,to) for the given
// employees (nil = all); ok is false when there is none.
func (s *Store) FirstRequestAt(ctx context.Context, from, to time.Time, employeeIDs []int64) (first time.Time, ok bool, err error) {
	var t *time.Time
	err = s.Pool.QueryRow(ctx, `
		SELECT MIN(started_at) FROM requests
		WHERE started_at >= $1 AND started_at < $2 AND ($3::bigint[] IS NULL OR employee_id = ANY($3))`,
		from, to, nullableIDs(employeeIDs)).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}

// UsageSeries counts requests and tokens in [from,to) per bucket, buckets
// aligned to origin; every bucket in the window is present (empty ones as
// zeros) so the series draws as a continuous line.
func (s *Store) UsageSeries(ctx context.Context, from, to time.Time, employeeIDs []int64, bucket time.Duration, origin time.Time) ([]SeriesPoint, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT date_bin($1::interval, started_at, $2) AS b, COUNT(*), COALESCE(SUM(tokens_input + tokens_output), 0)
		FROM requests
		WHERE started_at >= $3 AND started_at < $4 AND ($5::bigint[] IS NULL OR employee_id = ANY($5))
		GROUP BY b ORDER BY b`,
		fmt.Sprintf("%d seconds", int64(bucket/time.Second)), origin, from, to, nullableIDs(employeeIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[int64]SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.T, &p.Requests, &p.Tokens); err != nil {
			return nil, err
		}
		got[p.T.Unix()] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start := from.Add(-((from.Sub(origin)%bucket + bucket) % bucket)) // floor to the bucket grid
	var out []SeriesPoint
	for t := start; t.Before(to); t = t.Add(bucket) {
		p, ok := got[t.Unix()]
		if !ok {
			p = SeriesPoint{T: t}
		}
		out = append(out, p)
	}
	return out, nil
}

// ModelUsage counts an account's requests per requested model since a time.
func (s *Store) ModelUsage(ctx context.Context, accountID int64, since time.Time) (map[string]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT model_requested, COUNT(*) FROM requests
		WHERE account_id = $1 AND started_at >= $2 AND model_requested <> ''
		GROUP BY 1`, accountID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var m string
		var n int64
		if err := rows.Scan(&m, &n); err != nil {
			return nil, err
		}
		out[m] = n
	}
	return out, rows.Err()
}
