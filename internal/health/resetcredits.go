package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"aiseat/internal/cpa"
	"aiseat/internal/store"

	"github.com/google/uuid"
)

// ChatGPT grants some accounts rate-limit reset credits (重置卡): spending one
// resets the account's 5-hour and weekly windows at once. Codex offers them to
// people signed in with ChatGPT; employees reach Codex through the gateway,
// so the portal lists and spends them instead, through CPA like the quota
// probe (the account's own token and egress IP).
const (
	CodexResetCreditsURL       = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	CodexResetCreditConsumeURL = CodexResetCreditsURL + "/consume"
)

// ResetCredit is one reset credit as ChatGPT describes it.
type ResetCredit struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// ResetCredits is an account's reset credits; Available counts the usable ones.
type ResetCredits struct {
	Available int           `json:"available"`
	Credits   []ResetCredit `json:"credits"`
}

// Outcomes of spending a credit, as ChatGPT reports them.
const (
	ResetDone           = "reset"
	ResetNothingToReset = "nothing_to_reset"
	ResetNoCredit       = "no_credit"
	ResetAlreadyUsed    = "already_redeemed"
)

var ErrNoResetCredits = errors.New("this account type has no reset credits")

// chatgptCall makes one call to ChatGPT's backend as the account, the way
// the Codex CLI would.
func (p *QuotaProber) chatgptCall(ctx context.Context, acc store.Account, method, url, body string) (cpa.APICallResponse, error) {
	files, err := p.authFiles(ctx)
	if err != nil {
		return cpa.APICallResponse{}, err
	}
	f, ok := files[acc.CPAAuthName]
	if !ok || f.AuthIndex == "" {
		return cpa.APICallResponse{}, fmt.Errorf("auth file %s not found in CPA", acc.CPAAuthName)
	}
	call := cpa.APICallRequest{AuthIndex: f.AuthIndex, Method: method, URL: url, Data: body,
		Header: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Accept":        "application/json",
			"User-Agent":    p.userAgent(ctx, acc),
		}}
	if body != "" {
		call.Header["Content-Type"] = "application/json"
	}
	if id := f.IDToken.ChatGPTAccountID; id != "" {
		call.Header["ChatGPT-Account-Id"] = id
	}
	resp, err := p.CPA.APICallFresh(ctx, acc.CPAAuthName, call)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode != 200 {
		return resp, fmt.Errorf("%s returned %d: %.200s", url, resp.StatusCode, resp.Body)
	}
	return resp, nil
}

// ResetCredits lists the account's reset credits.
func (p *QuotaProber) ResetCredits(ctx context.Context, acc store.Account) (ResetCredits, error) {
	if !acc.IsChatGPT() {
		return ResetCredits{}, ErrNoResetCredits
	}
	resp, err := p.chatgptCall(ctx, acc, "GET", CodexResetCreditsURL, "")
	if err != nil {
		return ResetCredits{}, err
	}
	var out ResetCredits
	var raw struct {
		Available int           `json:"available_count"`
		Credits   []ResetCredit `json:"credits"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &raw); err != nil {
		return ResetCredits{}, fmt.Errorf("reset credits: %w", err)
	}
	out.Available = raw.Available
	for _, c := range raw.Credits {
		if c.Status == "available" {
			out.Credits = append(out.Credits, c)
		}
	}
	return out, nil
}

// AvailableResetCredits counts an account's usable reset credits (0 for
// accounts that have none by nature).
func (p *QuotaProber) AvailableResetCredits(ctx context.Context, accountID int64) (int, error) {
	acc, err := p.Store.GetAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	if !acc.IsChatGPT() {
		return 0, nil
	}
	c, err := p.ResetCredits(ctx, acc)
	return c.Available, err
}

// UseResetCredit spends one of the account's reset credits, the one expiring
// first. On "reset" the account is usable again at once: the gateway stops
// treating it as rate limited and takes a fresh quota snapshot.
func (p *QuotaProber) UseResetCredit(ctx context.Context, acc store.Account) (string, error) {
	lockValue, _ := p.creditLocks.LoadOrStore(acc.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	credits, err := p.ResetCredits(ctx, acc)
	if err != nil {
		return "", err
	}
	if len(credits.Credits) == 0 {
		return ResetNoCredit, nil
	}
	first := credits.Credits[0]
	for _, c := range credits.Credits[1:] {
		if c.ExpiresAt != nil && (first.ExpiresAt == nil || c.ExpiresAt.Before(*first.ExpiresAt)) {
			first = c
		}
	}
	body, _ := json.Marshal(map[string]string{"redeem_request_id": uuid.NewString(), "credit_id": first.ID})
	resp, err := p.chatgptCall(ctx, acc, "POST", CodexResetCreditConsumeURL, string(body))
	if err != nil {
		return "", err
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &out); err != nil || out.Code == "" {
		return "", fmt.Errorf("reset credit: unexpected answer %.200s", resp.Body)
	}
	if out.Code == ResetDone {
		// The credit has already been spent. Finish reconciliation even if the
		// caller disconnects, and report partial failure without suggesting retry.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		finished := false
		var end func(bool)
		if p.BeginAccountReset != nil {
			end = p.BeginAccountReset(acc.ID)
			defer func() {
				if end != nil {
					end(finished)
				}
			}()
		}
		files, err := p.authFiles(finishCtx)
		if err != nil {
			return out.Code, fmt.Errorf("reset succeeded, list credentials: %w", err)
		}
		f, ok := files[acc.CPAAuthName]
		if !ok || f.AuthIndex == "" {
			return out.Code, errors.New("reset succeeded, CPA credential not found")
		}
		if err := p.CPA.ResetQuota(finishCtx, f.AuthIndex); err != nil {
			return out.Code, fmt.Errorf("reset succeeded, clear CPA cooldown: %w", err)
		}
		if err := p.Store.SetRateLimitedUntil(finishCtx, acc.ID, nil); err != nil {
			return out.Code, fmt.Errorf("reset succeeded, clear gateway cooldown: %w", err)
		}
		finished = true
		if end != nil {
			end(true)
			end = nil
		}
		if _, err := p.probe(finishCtx, acc, files); err != nil {
			p.logger().Warn("reset credit: refresh quota", "account", acc.ID, "err", err)
		}
	}
	return out.Code, nil
}

// ErrQuotaStillLimited means the upstream has not confirmed that quota is available.
var ErrQuotaStillLimited = errors.New("upstream quota is limited or its status is unclear")

// ReconcileRateLimit clears stale routing cooldowns only after a fresh upstream
// response explicitly confirms that the account's quota is available.
func (p *QuotaProber) ReconcileRateLimit(ctx context.Context, acc store.Account) error {
	if !acc.IsChatGPT() {
		return ErrNoResetCredits
	}
	lockValue, _ := p.creditLocks.LoadOrStore(acc.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	finish := func(bool) {}
	if p.BeginAccountReset != nil {
		finish = p.BeginAccountReset(acc.ID)
	}
	ok := false
	defer func() { finish(ok) }()
	resp, err := p.chatgptCall(ctx, acc, "GET", CodexUsageURL, "")
	if err != nil {
		return err
	}
	var raw struct {
		RateLimit *struct {
			Allowed      *bool `json:"allowed"`
			LimitReached *bool `json:"limit_reached"`
		} `json:"rate_limit"`
	}
	if json.Unmarshal([]byte(resp.Body), &raw) != nil || raw.RateLimit == nil || raw.RateLimit.Allowed == nil || !*raw.RateLimit.Allowed || raw.RateLimit.LimitReached == nil || *raw.RateLimit.LimitReached {
		return ErrQuotaStillLimited
	}
	q, _, err := ParseCodexUsage([]byte(resp.Body), time.Now())
	if err != nil || (q.PrimaryUsedPct == nil && q.SecondaryUsedPct == nil) || (q.PrimaryUsedPct != nil && *q.PrimaryUsedPct >= 100) || (q.SecondaryUsedPct != nil && *q.SecondaryUsedPct >= 100) {
		return ErrQuotaStillLimited
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	files, err := p.authFiles(finishCtx)
	if err != nil {
		return err
	}
	file, exists := files[acc.CPAAuthName]
	if !exists || file.AuthIndex == "" {
		return errors.New("CPA credential not found")
	}
	if err := p.CPA.ResetQuota(finishCtx, file.AuthIndex); err != nil {
		return err
	}
	if err := p.Store.SetRateLimitedUntil(finishCtx, acc.ID, nil); err != nil {
		return err
	}
	ok = true
	return nil
}
