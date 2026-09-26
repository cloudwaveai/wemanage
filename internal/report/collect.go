// Package report builds and pushes the per-employee AI daily report. All
// numeric facts come from SQL (the store); the LLM is used only for prose.
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

// Stats holds the numeric facts for a day. Every value originates from SQL,
// never from the model.
type Stats struct {
	Requests        int64                  `json:"requests"`
	TokensInput     int64                  `json:"tokens_input"`
	TokensOutput    int64                  `json:"tokens_output"`
	TokensReasoning int64                  `json:"tokens_reasoning"`
	ByModel         []store.UsageBreakdown `json:"by_model"`
	ByClient        []store.UsageBreakdown `json:"by_client"`
	ActiveHours     []int                  `json:"active_hours"` // 24-length histogram of request counts by hour (in loc)
}

// Material is everything needed to write one employee's report for one day.
type Material struct {
	EmployeeID int64
	Date       time.Time
	Stats      Stats           // numeric, from SQL
	Sessions   []SessionDigest // conversation text grouped by session, chronological
}

// Collect gathers the day's material for one employee. loc defines the day
// boundary; the window is [date 00:00 loc, +24h).
func Collect(ctx context.Context, st *store.Store, employeeID int64, date time.Time, loc *time.Location) (Material, error) {
	if loc == nil {
		loc = time.UTC
	}
	from := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	to := from.Add(24 * time.Hour)

	m := Material{EmployeeID: employeeID, Date: date}

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

	hours, err := activeHours(ctx, st, employeeID, from, to, loc)
	if err != nil {
		return Material{}, err
	}
	m.Stats.ActiveHours = hours

	sessions, err := sessionDigests(ctx, st, employeeID, from, to)
	if err != nil {
		return Material{}, err
	}
	m.Sessions = sessions

	return m, nil
}

// activeHours returns a 24-length histogram of request counts by local hour.
func activeHours(ctx context.Context, st *store.Store, employeeID int64, from, to time.Time, loc *time.Location) ([]int, error) {
	hist := make([]int, 24)
	rows, err := st.Pool.Query(ctx, `
		SELECT EXTRACT(HOUR FROM started_at AT TIME ZONE $1)::int AS hr, COUNT(*)
		FROM requests
		WHERE employee_id = $2 AND started_at >= $3 AND started_at < $4
		GROUP BY hr`, loc.String(), employeeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hr int
		var n int
		if err := rows.Scan(&hr, &n); err != nil {
			return nil, err
		}
		if hr >= 0 && hr < 24 {
			hist[hr] = n
		}
	}
	return hist, rows.Err()
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
