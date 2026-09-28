package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// Usage endpoints the official CLIs read for their /status (/usage) views.
const (
	// CodexUsageURL returns a ChatGPT plan's rate-limit windows.
	CodexUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	// KimiUsageURL returns a Kimi Code membership's 5-hour and weekly usage.
	KimiUsageURL = "https://api.kimi.com/coding/v1/usages"
)

// ErrRefreshTooSoon is returned by Refresh within a minute of the last probe.
var ErrRefreshTooSoon = errors.New("quota was refreshed less than a minute ago")

// QuotaProber keeps accounts' 5-hour / weekly quota current by asking the
// upstream the way its official CLI does (Codex /status, Kimi /usage). The
// call goes through CPA, so it uses the account's own token and egress IP.
// Busy ChatGPT accounts already get snapshots from their own traffic; only
// stale ones are probed.
type QuotaProber struct {
	Store    *store.Store
	CPA      *cpa.Client
	Settings *settings.Service
	Log      *slog.Logger
	// MaxAge is how old the newest snapshot may get before a probe (default 30m).
	MaxAge time.Duration

	mu   sync.Mutex
	last map[int64]time.Time // accountID -> last probe, for Refresh's rate limit
}

// RunStale probes every active, logged-in account with a quota endpoint whose
// newest snapshot is older than MaxAge or predates a window reset. One
// failure does not stop the others.
func (p *QuotaProber) RunStale(ctx context.Context) error {
	maxAge := p.MaxAge
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	rows, err := p.Store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	var ids []int64
	for _, a := range rows {
		if probeable(a.Account) {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	latest, err := p.Store.LatestQuotas(ctx, ids)
	if err != nil {
		return err
	}
	files, err := p.authFiles(ctx)
	if err != nil {
		return err
	}
	for _, a := range rows {
		q, seen := latest[a.ID]
		if !probeable(a.Account) || (seen && time.Since(q.At) < maxAge && !resetSince(q, time.Now())) {
			continue
		}
		if _, err := p.probe(ctx, a.Account, files); err != nil {
			p.logger().Warn("quota probe", "account", a.ID, "err", err)
		}
		// Spread the calls out a little; nothing here is urgent.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

// Refresh probes one account now (an admin's "refresh" button), at most once
// a minute per account.
func (p *QuotaProber) Refresh(ctx context.Context, accountID int64) (store.QuotaSnapshot, error) {
	acc, err := p.Store.GetAccount(ctx, accountID)
	if err != nil {
		return store.QuotaSnapshot{}, err
	}
	if !hasQuotaEndpoint(acc) {
		return store.QuotaSnapshot{}, errors.New("this account type has no quota endpoint")
	}
	p.mu.Lock()
	if t, ok := p.last[accountID]; ok && time.Since(t) < time.Minute {
		p.mu.Unlock()
		return store.QuotaSnapshot{}, ErrRefreshTooSoon
	}
	p.mu.Unlock()
	files, err := p.authFiles(ctx)
	if err != nil {
		return store.QuotaSnapshot{}, err
	}
	return p.probe(ctx, acc, files)
}

// resetSince reports whether a window in q has reset since q was taken, so
// the snapshot no longer describes the current window. A snapshot taken after
// its own reset time does not count, so an upstream that keeps reporting a
// past reset cannot cause a probe every round.
func resetSince(q store.QuotaSnapshot, now time.Time) bool {
	for _, r := range []*time.Time{q.PrimaryResetAt, q.SecondaryResetAt} {
		if r != nil && r.Before(now) && q.At.Before(*r) {
			return true
		}
	}
	return false
}

// hasQuotaEndpoint reports whether the account's upstream exposes its quota.
func hasQuotaEndpoint(a store.Account) bool {
	return a.IsChatGPT() || a.Provider == store.ProviderKimi
}

func probeable(a store.Account) bool {
	return hasQuotaEndpoint(a) && a.Status == "active" && !a.NeedsLogin
}

func (p *QuotaProber) authFiles(ctx context.Context) (map[string]cpa.AuthFile, error) {
	list, err := p.CPA.ListAuthFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list CPA auth files: %w", err)
	}
	out := make(map[string]cpa.AuthFile, len(list))
	for _, f := range list {
		out[f.Name] = f
	}
	return out, nil
}

// probe fetches and stores one account's quota snapshot.
func (p *QuotaProber) probe(ctx context.Context, acc store.Account, files map[string]cpa.AuthFile) (store.QuotaSnapshot, error) {
	p.mu.Lock()
	if p.last == nil {
		p.last = map[int64]time.Time{}
	}
	p.last[acc.ID] = time.Now()
	p.mu.Unlock()

	f, ok := files[acc.CPAAuthName]
	if !ok || f.AuthIndex == "" {
		return store.QuotaSnapshot{}, fmt.Errorf("auth file %s not found in CPA", acc.CPAAuthName)
	}
	call := cpa.APICallRequest{AuthIndex: f.AuthIndex, Method: "GET"}
	if acc.IsChatGPT() {
		call.URL = CodexUsageURL
		call.Header = map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Accept":        "application/json",
			"User-Agent":    p.userAgent(ctx, acc),
		}
		if id := f.IDToken.ChatGPTAccountID; id != "" {
			call.Header["ChatGPT-Account-Id"] = id
		}
	} else {
		// kimi-cli's /usage sends only the bearer token.
		call.URL = KimiUsageURL
		call.Header = map[string]string{"Authorization": "Bearer $TOKEN$"}
	}
	resp, err := p.CPA.APICall(ctx, call)
	if err != nil {
		return store.QuotaSnapshot{}, err
	}
	if resp.StatusCode != 200 {
		return store.QuotaSnapshot{}, fmt.Errorf("usage endpoint returned %d: %.200s", resp.StatusCode, resp.Body)
	}
	var q store.QuotaSnapshot
	plan := ""
	if acc.IsChatGPT() {
		q, plan, err = ParseCodexUsage([]byte(resp.Body), time.Now())
	} else {
		q, err = ParseKimiUsage([]byte(resp.Body), time.Now())
	}
	if err != nil {
		return store.QuotaSnapshot{}, err
	}
	q.AccountID = acc.ID
	if err := p.Store.InsertQuota(ctx, q); err != nil {
		return store.QuotaSnapshot{}, err
	}
	if plan != "" && plan != acc.PlanType {
		if err := p.Store.UpdateAccount(ctx, acc.ID, store.AccountPatch{PlanType: &plan}); err != nil {
			p.logger().Warn("quota probe: update plan", "account", acc.ID, "err", err)
		}
	}
	return q, nil
}

// userAgent is the account's latest real Codex User-Agent, so the probe looks
// like the client that account actually uses; before any traffic it falls
// back to the configured Codex profile.
func (p *QuotaProber) userAgent(ctx context.Context, acc store.Account) string {
	if ua, err := p.Store.LatestCodexUA(ctx, acc.ID); err == nil && ua != "" {
		return ua
	}
	prof := p.Settings.Get(ctx).CodexProfile
	return strings.NewReplacer(
		"{version}", prof.Version, "{os}", acc.DeviceProfile.OS, "{terminal}", acc.DeviceProfile.Terminal,
	).Replace(prof.UATemplate)
}

func (p *QuotaProber) logger() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// usageWindow is one rate-limit window in the usage response.
type usageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

// ParseCodexUsage converts a /wham/usage response into a snapshot (5-hour
// window primary, weekly secondary, see NormalizeQuotaWindows) and returns
// the plan type.
func ParseCodexUsage(body []byte, now time.Time) (store.QuotaSnapshot, string, error) {
	var r struct {
		PlanType  string `json:"plan_type"`
		RateLimit *struct {
			PrimaryWindow   *usageWindow `json:"primary_window"`
			SecondaryWindow *usageWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return store.QuotaSnapshot{}, "", fmt.Errorf("decode usage: %w", err)
	}
	if r.RateLimit == nil || (r.RateLimit.PrimaryWindow == nil && r.RateLimit.SecondaryWindow == nil) {
		return store.QuotaSnapshot{}, r.PlanType, errors.New("usage response has no rate-limit windows")
	}
	q := store.QuotaSnapshot{At: now}
	q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt = r.RateLimit.PrimaryWindow.fields(now)
	q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt = r.RateLimit.SecondaryWindow.fields(now)
	return NormalizeQuotaWindows(q), r.PlanType, nil
}

// fields maps a window onto snapshot columns; a nil window yields nils.
func (w *usageWindow) fields(now time.Time) (pct *float64, minutes *int, resetAt *time.Time) {
	if w == nil {
		return nil, nil, nil
	}
	pct = w.UsedPercent
	if w.LimitWindowSeconds != nil {
		m := int((*w.LimitWindowSeconds + 59) / 60)
		minutes = &m
	}
	switch {
	case w.ResetAt != nil && *w.ResetAt > 0:
		t := time.Unix(*w.ResetAt, 0)
		resetAt = &t
	case w.ResetAfterSeconds != nil:
		t := now.Add(time.Duration(*w.ResetAfterSeconds) * time.Second)
		resetAt = &t
	}
	return pct, minutes, resetAt
}

// kimiRatio is one window of the Kimi usages summary.
type kimiRatio struct {
	UsedRatio *float64 `json:"used_ratio"`
	ResetTime string   `json:"reset_time"`
}

// kimiDetail is a limit/used/remaining block; Kimi sends the numbers as strings.
type kimiDetail struct {
	Limit     json.Number `json:"limit"`
	Used      json.Number `json:"used"`
	Remaining json.Number `json:"remaining"`
	ResetTime string      `json:"resetTime"`
}

// ParseKimiUsage converts a Kimi Code /usages response into a snapshot. It
// reads the "usages" summary (limit_5h / limit_7d as used ratios) and falls
// back to the per-window "limits" and the weekly "usage" block that kimi-cli
// displays.
func ParseKimiUsage(body []byte, now time.Time) (store.QuotaSnapshot, error) {
	var r struct {
		Usages struct {
			Limit5h *kimiRatio `json:"limit_5h"`
			Limit7d *kimiRatio `json:"limit_7d"`
		} `json:"usages"`
		Usage  *kimiDetail `json:"usage"`
		Limits []struct {
			Window struct {
				Duration json.Number `json:"duration"`
				TimeUnit string      `json:"timeUnit"`
			} `json:"window"`
			Detail *kimiDetail `json:"detail"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return store.QuotaSnapshot{}, fmt.Errorf("decode kimi usage: %w", err)
	}
	q := store.QuotaSnapshot{At: now}
	five, week := fiveHourMinutes, weekMinutes
	if w := r.Usages.Limit5h; w != nil && w.UsedRatio != nil {
		q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt = ratioPct(*w.UsedRatio), &five, parseTime(w.ResetTime)
	}
	if w := r.Usages.Limit7d; w != nil && w.UsedRatio != nil {
		q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt = ratioPct(*w.UsedRatio), &week, parseTime(w.ResetTime)
	}
	if q.PrimaryUsedPct == nil {
		for _, l := range r.Limits {
			minutes := windowMinutes(l.Window.Duration, l.Window.TimeUnit)
			if l.Detail == nil || minutes == 0 || minutes > 24*60 {
				continue
			}
			if pct := l.Detail.pct(); pct != nil {
				q.PrimaryUsedPct, q.PrimaryWindowMinutes, q.PrimaryResetAt = pct, &minutes, parseTime(l.Detail.ResetTime)
				break
			}
		}
	}
	if q.SecondaryUsedPct == nil && r.Usage != nil {
		if pct := r.Usage.pct(); pct != nil {
			q.SecondaryUsedPct, q.SecondaryWindowMinutes, q.SecondaryResetAt = pct, &week, parseTime(r.Usage.ResetTime)
		}
	}
	if q.PrimaryUsedPct == nil && q.SecondaryUsedPct == nil {
		return store.QuotaSnapshot{}, errors.New("kimi usage response has no limits")
	}
	return NormalizeQuotaWindows(q), nil
}

// pct is used/limit as a percentage (used = limit - remaining when absent).
func (d *kimiDetail) pct() *float64 {
	limit, err := d.Limit.Float64()
	if err != nil || limit <= 0 {
		return nil
	}
	used, err := d.Used.Float64()
	if err != nil {
		rem, rerr := d.Remaining.Float64()
		if rerr != nil {
			return nil
		}
		used = limit - rem
	}
	v := used / limit * 100
	return &v
}

func ratioPct(r float64) *float64 {
	v := r * 100
	return &v
}

// windowMinutes converts a Kimi window ({300, TIME_UNIT_MINUTE}) to minutes.
func windowMinutes(d json.Number, unit string) int {
	n, err := d.Int64()
	if err != nil || n <= 0 {
		return 0
	}
	switch u := strings.ToUpper(unit); {
	case strings.Contains(u, "MINUTE"):
		return int(n)
	case strings.Contains(u, "HOUR"):
		return int(n) * 60
	case strings.Contains(u, "DAY"):
		return int(n) * 24 * 60
	}
	return 0
}

func parseTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}
