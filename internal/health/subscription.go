package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aiseat/internal/store"
)

// A ChatGPT account's subscription (when it next renews, or ends if it was
// cancelled) comes from ChatGPT's account check, the call the ChatGPT apps
// make at start, through CPA like the quota probe: the account's own token
// and egress IP. When the check fails, the id_token's paid-until date (as of
// the last token refresh) stands in until it works.
const CodexAccountsCheckURL = "https://chatgpt.com/backend-api/accounts/check/v4-2023-04-27"

// SubscriptionMaxAge is how old a subscription check may get before the
// background job checks again; a failed check is retried sooner.
const (
	SubscriptionMaxAge   = 6 * time.Hour
	SubscriptionRetryAge = 30 * time.Minute
)

// subscriptionGap spaces the background checks: several accounts asked at
// once got ChatGPT's 403 page together (2026-10-08).
var subscriptionGap = 5 * time.Second

var ErrNoSubscription = errors.New("this account type has no subscription to check")

// CheckSubscription asks ChatGPT about the account's subscription and saves
// what it says.
func (p *QuotaProber) CheckSubscription(ctx context.Context, acc store.Account) (store.Subscription, error) {
	if !acc.IsChatGPT() {
		return store.Subscription{}, ErrNoSubscription
	}
	sub, err := p.fetchSubscription(ctx, acc)
	if err != nil {
		fallback := store.Subscription{AccountID: acc.ID}
		if files, ferr := p.authFiles(ctx); ferr == nil {
			if f, ok := files[acc.CPAAuthName]; ok {
				fallback.Plan = f.IDToken.PlanType
				if t, perr := time.Parse(time.RFC3339, f.IDToken.SubscriptionActiveUntil); perr == nil {
					fallback.ExpiresAt, fallback.Active = &t, t.After(time.Now())
				}
			}
		}
		_ = p.Store.SubscriptionCheckFailed(ctx, fallback, err.Error())
		return store.Subscription{}, err
	}
	sub.AccountID = acc.ID
	if err := p.Store.SaveSubscription(ctx, sub); err != nil {
		return store.Subscription{}, err
	}
	return sub, nil
}

func (p *QuotaProber) fetchSubscription(ctx context.Context, acc store.Account) (store.Subscription, error) {
	files, err := p.authFiles(ctx)
	if err != nil {
		return store.Subscription{}, err
	}
	resp, err := p.chatgptCall(ctx, acc, "GET", CodexAccountsCheckURL, "")
	if err != nil {
		return store.Subscription{}, err
	}
	return parseAccountsCheck([]byte(resp.Body), files[acc.CPAAuthName].IDToken.ChatGPTAccountID)
}

// parseAccountsCheck picks the account's entry (by ChatGPT account id, else
// the one with an active subscription, else any) from an accounts/check reply.
func parseAccountsCheck(body []byte, accountID string) (store.Subscription, error) {
	type entitlement struct {
		Plan       string     `json:"subscription_plan"`
		Active     bool       `json:"has_active_subscription"`
		RenewsAt   *time.Time `json:"renews_at"`
		ExpiresAt  *time.Time `json:"expires_at"`
		CancelsAt  *time.Time `json:"cancels_at"`
		Delinquent bool       `json:"is_delinquent"`
	}
	type entry struct {
		Account struct {
			PlanType string `json:"plan_type"`
		} `json:"account"`
		Entitlement *entitlement `json:"entitlement"`
	}
	var raw struct {
		Accounts map[string]entry `json:"accounts"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return store.Subscription{}, fmt.Errorf("accounts check: %w", err)
	}
	pick, found := raw.Accounts[accountID]
	if !found || pick.Entitlement == nil {
		found = false
		for _, e := range raw.Accounts {
			if e.Entitlement == nil {
				continue
			}
			if !found || (e.Entitlement.Active && !pick.Entitlement.Active) {
				pick, found = e, true
			}
		}
	}
	if !found {
		return store.Subscription{}, errors.New("accounts check: no subscription in the reply")
	}
	e := pick.Entitlement
	plan := pick.Account.PlanType
	if plan == "" {
		plan = e.Plan
	}
	return store.Subscription{
		Plan: plan, Active: e.Active, RenewsAt: e.RenewsAt, ExpiresAt: e.ExpiresAt,
		CancelsAt: e.CancelsAt, Delinquent: e.Delinquent,
	}, nil
}

// RunSubscriptions checks the ChatGPT accounts whose subscription was last
// checked more than SubscriptionMaxAge ago (or never).
func (p *QuotaProber) RunSubscriptions(ctx context.Context) error {
	rows, err := p.Store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	subs, err := p.Store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	first := true
	for _, a := range rows {
		if !a.IsChatGPT() || a.Status != "active" {
			continue
		}
		if s, ok := subs[a.ID]; ok {
			maxAge := SubscriptionMaxAge
			if s.Error != "" {
				maxAge = SubscriptionRetryAge
			}
			if time.Since(s.CheckedAt) < maxAge {
				continue
			}
		}
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(subscriptionGap):
			}
		}
		first = false
		if _, err := p.CheckSubscription(ctx, a.Account); err != nil && p.Log != nil {
			p.Log.Warn("subscription check", "account", a.ID, "err", err)
		}
	}
	return nil
}
