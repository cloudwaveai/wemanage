// Package health computes account display status, quota snapshots from CPA
// passthrough headers, egress-IP verification, and Codex client-version hints.
package health

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"aiseat/internal/store"
)

// Status is the derived, user-facing state of an account.
type Status string

// Display statuses in descending priority order.
const (
	StatusActive      Status = "active"
	StatusRateLimited Status = "rate_limited"
	StatusNeedsLogin  Status = "needs_login"
	StatusDegraded    Status = "degraded"
	StatusDisabled    Status = "disabled"
)

// EvaluateAccount computes the display status by priority:
// disabled(status=="disabled") > needs_login(NeedsLogin) >
// rate_limited(RateLimitedUntil>now) > degraded(open "degraded" signal) > active.
func EvaluateAccount(ctx context.Context, st *store.Store, accountID int64) (Status, error) {
	a, err := st.GetAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	if a.Status == "disabled" {
		return StatusDisabled, nil
	}
	if a.NeedsLogin {
		return StatusNeedsLogin, nil
	}
	if a.RateLimitedUntil != nil && a.RateLimitedUntil.After(time.Now()) {
		return StatusRateLimited, nil
	}
	degraded, err := st.HasOpenSignal(ctx, accountID, "degraded")
	if err != nil {
		return "", err
	}
	if degraded {
		return StatusDegraded, nil
	}
	return StatusActive, nil
}

// CPA passthrough quota header names.
const (
	hdrPrimaryUsedPct       = "X-Codex-Primary-Used-Percent"
	hdrPrimaryWindowMinutes = "X-Codex-Primary-Window-Minutes"
	hdrPrimaryResetAfter    = "X-Codex-Primary-Reset-After-Seconds"
	hdrPrimaryResetAt       = "X-Codex-Primary-Reset-At"

	hdrSecondaryUsedPct       = "X-Codex-Secondary-Used-Percent"
	hdrSecondaryWindowMinutes = "X-Codex-Secondary-Window-Minutes"
	hdrSecondaryResetAfter    = "X-Codex-Secondary-Reset-After-Seconds"
	hdrSecondaryResetAt       = "X-Codex-Secondary-Reset-At"
)

// RecordQuotaFromHeaders parses CPA passthrough quota headers into a
// QuotaSnapshot and stores it. Missing headers yield nil pointers. Reset-At is
// unix seconds; when only Reset-After-Seconds is present, reset = now + seconds.
// If neither the primary nor the secondary window is present, nothing is stored.
func RecordQuotaFromHeaders(ctx context.Context, st *store.Store, accountID int64, h http.Header, now time.Time) error {
	pUsed := parseFloatHeader(h, hdrPrimaryUsedPct)
	pWin := parseIntHeader(h, hdrPrimaryWindowMinutes)
	pReset := parseResetHeader(h, hdrPrimaryResetAt, hdrPrimaryResetAfter, now)

	sUsed := parseFloatHeader(h, hdrSecondaryUsedPct)
	sWin := parseIntHeader(h, hdrSecondaryWindowMinutes)
	sReset := parseResetHeader(h, hdrSecondaryResetAt, hdrSecondaryResetAfter, now)

	primaryPresent := pUsed != nil || pWin != nil || pReset != nil
	secondaryPresent := sUsed != nil || sWin != nil || sReset != nil
	if !primaryPresent && !secondaryPresent {
		return nil
	}

	return st.InsertQuota(ctx, NormalizeQuotaWindows(store.QuotaSnapshot{
		AccountID:              accountID,
		At:                     now,
		PrimaryUsedPct:         pUsed,
		PrimaryWindowMinutes:   pWin,
		PrimaryResetAt:         pReset,
		SecondaryUsedPct:       sUsed,
		SecondaryWindowMinutes: sWin,
		SecondaryResetAt:       sReset,
	}))
}

// HasQuotaHeaders reports whether a response carries any quota header.
func HasQuotaHeaders(h http.Header) bool {
	for _, n := range []string{hdrPrimaryUsedPct, hdrPrimaryWindowMinutes, hdrPrimaryResetAt, hdrPrimaryResetAfter,
		hdrSecondaryUsedPct, hdrSecondaryWindowMinutes, hdrSecondaryResetAt, hdrSecondaryResetAfter} {
		if h.Get(n) != "" {
			return true
		}
	}
	return false
}

func parseFloatHeader(h http.Header, name string) *float64 {
	v := h.Get(name)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &f
}

func parseIntHeader(h http.Header, name string) *int {
	v := h.Get(name)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &n
}

// parseResetHeader prefers an absolute unix-seconds Reset-At; otherwise it uses
// now + Reset-After-Seconds. Returns nil when neither is present or parseable.
func parseResetHeader(h http.Header, atName, afterName string, now time.Time) *time.Time {
	if v := h.Get(atName); v != "" {
		if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
			t := time.Unix(secs, 0).UTC()
			return &t
		}
	}
	if v := h.Get(afterName); v != "" {
		if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
			t := now.Add(time.Duration(secs) * time.Second)
			return &t
		}
	}
	return nil
}

// CheckDegradation records or resolves a "degraded" signal for an account.
// The account is considered degraded when either:
//   - the count of "model_mismatch" signals in the last 24h >= mismatchThreshold, or
//   - among the most recent reasoning samples, >=80% share one value AND that
//     value is <= 600 (a clamped/downgraded reasoning-effort budget).
//
// Reasoning samples are passed in so this stays testable without a requests store.
func CheckDegradation(ctx context.Context, st *store.Store, accountID int64, mismatchThreshold int, recentReasoning []int64, now time.Time) error {
	degraded, detail := isDegraded(recentReasoning)

	if !degraded && mismatchThreshold > 0 {
		since := now.Add(-24 * time.Hour)
		n, err := st.CountSignalsSince(ctx, accountID, "model_mismatch", since)
		if err != nil {
			return err
		}
		if n >= mismatchThreshold {
			degraded = true
			detail = map[string]any{"reason": "model_mismatch", "count": n, "threshold": mismatchThreshold}
		}
	}

	open, err := st.HasOpenSignal(ctx, accountID, "degraded")
	if err != nil {
		return err
	}

	switch {
	case degraded && !open:
		_, err := st.AddSignal(ctx, accountID, "degraded", detail)
		return err
	case !degraded && open:
		return st.ResolveSignalsOfKind(ctx, accountID, "degraded")
	}
	return nil
}

// isDegraded reports whether the reasoning samples show a dominant low value:
// >=80% of samples share a single value and that value is <= 600.
func isDegraded(samples []int64) (bool, map[string]any) {
	if len(samples) == 0 {
		return false, nil
	}
	counts := make(map[int64]int, len(samples))
	for _, v := range samples {
		counts[v]++
	}
	var bestVal int64
	var bestCount int
	for v, c := range counts {
		if c > bestCount {
			bestVal, bestCount = v, c
		}
	}
	share := float64(bestCount) / float64(len(samples))
	if share >= 0.8 && bestVal <= 600 {
		return true, map[string]any{
			"reason": "reasoning_downgrade",
			"value":  bestVal,
			"share":  share,
			"n":      len(samples),
		}
	}
	return false, nil
}
