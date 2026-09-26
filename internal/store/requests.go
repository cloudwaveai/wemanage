package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestRow is one logged request/response with prefix-encoded segment hashes.
type RequestRow struct {
	ID              [16]byte
	StartedAt       time.Time
	EmployeeID      int64
	AccountID       *int64
	APIKeyID        *int64
	SessionID       *int64
	ClientKind      string
	UserAgent       string
	Endpoint        string
	Stream          bool
	ModelRequested  string
	ModelResponded  string
	ReasoningEffort string
	StatusCode      int
	ErrorType       string
	ErrorBody       string
	QueuedMS        int
	TTFTMs          *int
	DurationMS      int
	TokensInput     int64
	TokensCached    int64
	TokensOutput    int64
	TokensReasoning int64
	ReqPrefixOf     *[16]byte
	ReqPrefixLen    int
	ReqChainDepth   int
	ReqHashes       []byte // encoded (tail when prefix-compressed, else full)
	ReqInputStart   int
	RespHashes      []byte // full list of response segment hashes
	BodiesPurged    bool
}

// RequestEnc is the minimal encoding needed to reconstruct a request's hash list.
type RequestEnc struct {
	PrefixOf   *[16]byte
	PrefixLen  int
	ChainDepth int
	Hashes     []byte
}

// InsertRequest stores a request row.
func (s *Store) InsertRequest(ctx context.Context, r RequestRow) error {
	var prefixOf *[]byte
	if r.ReqPrefixOf != nil {
		b := r.ReqPrefixOf[:]
		prefixOf = &b
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO requests (
			id, started_at, employee_id, account_id, api_key_id, session_id,
			client_kind, user_agent, endpoint, stream, model_requested, model_responded,
			reasoning_effort, status_code, error_type, error_body, queued_ms, ttft_ms, duration_ms,
			tokens_input, tokens_cached, tokens_output, tokens_reasoning,
			req_prefix_of, req_prefix_len, req_chain_depth, req_hashes, req_input_start, resp_hashes
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
			$20,$21,$22,$23,$24,$25,$26,$27,$28,$29
		)`,
		r.ID[:], r.StartedAt, r.EmployeeID, r.AccountID, r.APIKeyID, r.SessionID,
		r.ClientKind, r.UserAgent, r.Endpoint, r.Stream, r.ModelRequested, r.ModelResponded,
		r.ReasoningEffort, r.StatusCode, r.ErrorType, r.ErrorBody, r.QueuedMS, r.TTFTMs, r.DurationMS,
		r.TokensInput, r.TokensCached, r.TokensOutput, r.TokensReasoning,
		prefixOf, r.ReqPrefixLen, r.ReqChainDepth, r.ReqHashes, r.ReqInputStart, r.RespHashes)
	return err
}

// GetRequestEnc returns the request-hash encoding for reconstruction.
func (s *Store) GetRequestEnc(ctx context.Context, id [16]byte) (RequestEnc, error) {
	var enc RequestEnc
	var prefixOf *[]byte
	err := s.Pool.QueryRow(ctx, `
		SELECT req_prefix_of, req_prefix_len, req_chain_depth, req_hashes
		FROM requests WHERE id=$1`, id[:]).
		Scan(&prefixOf, &enc.PrefixLen, &enc.ChainDepth, &enc.Hashes)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestEnc{}, ErrNotFound
	}
	if err != nil {
		return RequestEnc{}, err
	}
	if prefixOf != nil && len(*prefixOf) == 16 {
		var u [16]byte
		copy(u[:], *prefixOf)
		enc.PrefixOf = &u
	}
	return enc, nil
}

// InsertTexts bulk-inserts extracted searchable texts.
func (s *Store) InsertTexts(ctx context.Context, rows []TextRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, t := range rows {
		batch.Queue(`
			INSERT INTO request_texts (request_id, employee_id, session_id, started_at, role, text)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			t.RequestID[:], t.EmployeeID, t.SessionID, t.StartedAt, t.Role, t.Text)
	}
	br := s.Pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// TextRow is a searchable text extracted from a request/response.
type TextRow struct {
	RequestID  [16]byte
	EmployeeID int64
	SessionID  *int64
	StartedAt  time.Time
	Role       string
	Text       string
}

// MarkPurgedRequests flags requests whose bodies were purged.
func (s *Store) MarkPurgedRequests(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE requests SET bodies_purged=TRUE WHERE started_at < $1 AND bodies_purged=FALSE`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteExpiredTexts removes searchable text rows older than before.
func (s *Store) DeleteExpiredTexts(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM request_texts WHERE started_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
