// Package report builds the weekly AI-use recap each employee receives
// (only they do, and they can turn it off). All numeric facts come from SQL
// (the store); the LLM is used only for prose.
package report

import (
	"context"
	"time"

	"aiseat/internal/store"
)

// maxTextRunes bounds a single stored conversation turn (rune-safe).
const maxTextRunes = 1500

// Turn is a single conversation message.
type Turn struct {
	Role string
	Text string
}

// SessionDigest is one session's turns in chronological order.
type SessionDigest struct {
	SessionID int64
	Texts     []Turn
}

// Stats holds the numeric facts for a period. Every value originates from
// SQL, never from the model. There is deliberately no time-of-day breakdown:
// the recap is about what AI helped with, not when someone worked.
type Stats struct {
	Requests        int64                  `json:"requests"`
	TokensInput     int64                  `json:"tokens_input"`
	TokensOutput    int64                  `json:"tokens_output"`
	TokensReasoning int64                  `json:"tokens_reasoning"`
	ByModel         []store.UsageBreakdown `json:"by_model"`
	ByClient        []store.UsageBreakdown `json:"by_client"`
}

// Material is everything needed to write one employee's report for a period.
type Material struct {
	EmployeeID int64
	From, To   time.Time
	Stats      Stats           // numeric, from SQL
	Sessions   []SessionDigest // conversation text grouped by session, chronological
}

// Collect gathers one employee's material for [from, to).
func Collect(ctx context.Context, st *store.Store, employeeID int64, from, to time.Time) (Material, error) {
	m := Material{EmployeeID: employeeID, From: from, To: to}

	u, err := st.Usage(ctx, from, to, []int64{employeeID})
	if err != nil {
		return Material{}, err
	}
	m.Stats = Stats{
		Requests:        u.Requests,
		TokensInput:     u.TokensInput,
		TokensOutput:    u.TokensOutput,
		TokensReasoning: u.TokensReasoning,
		ByModel:         u.ByModel,
		ByClient:        u.ByClient,
	}

	sessions, err := sessionDigests(ctx, st, employeeID, from, to)
	if err != nil {
		return Material{}, err
	}
	m.Sessions = sessions
	return m, nil
}

// sessionDigests loads the employee's conversation texts in the window and
// groups them into per-session digests, chronological within each session.
func sessionDigests(ctx context.Context, st *store.Store, employeeID int64, from, to time.Time) ([]SessionDigest, error) {
	rows, err := st.Pool.Query(ctx, `
		SELECT session_id, role, text
		FROM request_texts
		WHERE employee_id = $1 AND started_at >= $2 AND started_at < $3
		ORDER BY session_id NULLS FIRST, started_at, id`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionDigest
	var cur *SessionDigest
	haveCur := false
	var curID int64
	for rows.Next() {
		var sid *int64
		var role, text string
		if err := rows.Scan(&sid, &role, &text); err != nil {
			return nil, err
		}
		var id int64
		if sid != nil {
			id = *sid
		}
		if !haveCur || id != curID {
			out = append(out, SessionDigest{SessionID: id})
			cur = &out[len(out)-1]
			curID = id
			haveCur = true
		}
		cur.Texts = append(cur.Texts, Turn{Role: role, Text: truncateRunes(text, maxTextRunes)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// truncateRunes returns s limited to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
