package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestListItem is a row in the log-search results.
type RequestListItem struct {
	ID             [16]byte
	StartedAt      time.Time
	EmployeeID     int64
	EmployeeName   string
	ClientKind     string
	Endpoint       string
	ModelRequested string
	ModelResponded string
	StatusCode     int
	TokensInput    int64
	TokensOutput   int64
	DurationMS     int
}

// RequestFilter narrows a log search. Empty fields are ignored.
type RequestFilter struct {
	EmployeeIDs []int64 // nil = all (subject to caller's RBAC scope)
	From        time.Time
	To          time.Time
	Query       string // substring match against extracted texts
	Client      string
	Model       string
	Limit       int
	Offset      int
}

// ListRequests returns a page of matching requests (newest first) and the total count.
func (s *Store) ListRequests(ctx context.Context, f RequestFilter) ([]RequestListItem, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := "WHERE 1=1"
	args := []any{}
	if f.EmployeeIDs != nil {
		args = append(args, f.EmployeeIDs)
		where += " AND r.employee_id = ANY($" + itoa(int64(len(args))) + ")"
	}
	if !f.From.IsZero() {
		args = append(args, f.From)
		where += " AND r.started_at >= $" + itoa(int64(len(args)))
	}
	if !f.To.IsZero() {
		args = append(args, f.To)
		where += " AND r.started_at < $" + itoa(int64(len(args)))
	}
	if f.Client != "" {
		args = append(args, f.Client)
		where += " AND r.client_kind = $" + itoa(int64(len(args)))
	}
	if f.Model != "" {
		args = append(args, f.Model)
		where += " AND r.model_requested = $" + itoa(int64(len(args)))
	}
	if strings.TrimSpace(f.Query) != "" {
		args = append(args, "%"+f.Query+"%")
		where += " AND EXISTS (SELECT 1 FROM request_texts rt WHERE rt.request_id = r.id AND rt.text ILIKE $" + itoa(int64(len(args))) + ")"
	}

	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requests r `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit)
	limIdx := itoa(int64(len(args)))
	args = append(args, f.Offset)
	offIdx := itoa(int64(len(args)))

	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, r.started_at, r.employee_id, COALESCE(e.name,''), r.client_kind, r.endpoint,
			r.model_requested, r.model_responded, r.status_code, r.tokens_input, r.tokens_output, r.duration_ms
		FROM requests r LEFT JOIN employees e ON e.id = r.employee_id
		`+where+`
		ORDER BY r.started_at DESC LIMIT $`+limIdx+` OFFSET $`+offIdx, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []RequestListItem
	for rows.Next() {
		var it RequestListItem
		var idb []byte
		if err := rows.Scan(&idb, &it.StartedAt, &it.EmployeeID, &it.EmployeeName, &it.ClientKind, &it.Endpoint,
			&it.ModelRequested, &it.ModelResponded, &it.StatusCode, &it.TokensInput, &it.TokensOutput, &it.DurationMS); err != nil {
			return nil, 0, err
		}
		copy(it.ID[:], idb)
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// GetRequestFull returns a full request row for reconstruction.
func (s *Store) GetRequestFull(ctx context.Context, id [16]byte) (RequestRow, error) {
	var r RequestRow
	var idb, prefixOf []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT id, started_at, employee_id, account_id, api_key_id, session_id, client_kind,
			user_agent, endpoint, stream, model_requested, model_responded, reasoning_effort,
			status_code, error_type, error_body, queued_ms, ttft_ms, duration_ms,
			tokens_input, tokens_cached, tokens_output, tokens_reasoning,
			req_prefix_of, req_prefix_len, req_chain_depth, req_hashes, req_input_start, resp_hashes, bodies_purged
		FROM requests WHERE id=$1`, id[:]).
		Scan(&idb, &r.StartedAt, &r.EmployeeID, &r.AccountID, &r.APIKeyID, &r.SessionID, &r.ClientKind,
			&r.UserAgent, &r.Endpoint, &r.Stream, &r.ModelRequested, &r.ModelResponded, &r.ReasoningEffort,
			&r.StatusCode, &r.ErrorType, &r.ErrorBody, &r.QueuedMS, &r.TTFTMs, &r.DurationMS,
			&r.TokensInput, &r.TokensCached, &r.TokensOutput, &r.TokensReasoning,
			&prefixOf, &r.ReqPrefixLen, &r.ReqChainDepth, &r.ReqHashes, &r.ReqInputStart, &r.RespHashes, &r.BodiesPurged)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestRow{}, ErrNotFound
	}
	if err != nil {
		return RequestRow{}, err
	}
	copy(r.ID[:], idb)
	if len(prefixOf) == 16 {
		var u [16]byte
		copy(u[:], prefixOf)
		r.ReqPrefixOf = &u
	}
	return r, nil
}
