package store

import (
	"context"
	"time"
)

// SessionPrev carries the previous request's state for a session, used to
// prefix-compress the next request's hash list.
type SessionPrev struct {
	ID             int64
	PrevRequestID  *[16]byte
	PrevFull       [][32]byte // decoded full hash list of the previous request
	PrevChainDepth int
	PrevInputCount int
}

// BeginSession finds or creates the session for (employee, sessionKey) and returns
// its id plus the previous request's state. It does not mutate counters.
func (s *Store) BeginSession(ctx context.Context, employeeID int64, accountID *int64, sessionKey, clientKind string, at time.Time) (SessionPrev, error) {
	var sp SessionPrev
	var prevID *[]byte
	var fullBytes []byte
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO sessions (employee_id, account_id, session_key, client_kind, first_at, last_at)
		VALUES ($1,$2,$3,$4,$5,$5)
		ON CONFLICT (employee_id, session_key) DO UPDATE SET account_id=EXCLUDED.account_id
		RETURNING id, last_request_id, last_req_hashes, last_req_chain_depth, last_input_count`,
		employeeID, accountID, sessionKey, clientKind, at).
		Scan(&sp.ID, &prevID, &fullBytes, &sp.PrevChainDepth, &sp.PrevInputCount)
	if err != nil {
		return SessionPrev{}, err
	}
	if prevID != nil && len(*prevID) == 16 {
		var u [16]byte
		copy(u[:], *prevID)
		sp.PrevRequestID = &u
	}
	sp.PrevFull = splitHashes(fullBytes)
	return sp, nil
}

// UpdateSessionAfterRequest stores the new full hash list and bumps counters.
func (s *Store) UpdateSessionAfterRequest(ctx context.Context, sessionID int64, reqID [16]byte, fullHashes []byte, chainDepth, inputCount int, at time.Time) error {
	_, err := s.Pool.Exec(ctx, updateSessionSQL, sessionID, reqID[:], fullHashes, chainDepth, inputCount, at)
	return err
}

const updateSessionSQL = `
	UPDATE sessions SET
		last_request_id=$2, last_req_hashes=$3, last_req_chain_depth=$4,
		last_input_count=$5, last_at=$6, request_count=request_count+1
	WHERE id=$1`

// splitHashes turns concatenated 32-byte hashes into a slice.
func splitHashes(b []byte) [][32]byte {
	n := len(b) / 32
	if n == 0 {
		return nil
	}
	out := make([][32]byte, n)
	for i := 0; i < n; i++ {
		copy(out[i][:], b[i*32:(i+1)*32])
	}
	return out
}

// JoinHashes concatenates 32-byte hashes into a flat byte slice.
func JoinHashes(hashes [][32]byte) []byte {
	out := make([]byte, 0, len(hashes)*32)
	for i := range hashes {
		out = append(out, hashes[i][:]...)
	}
	return out
}
