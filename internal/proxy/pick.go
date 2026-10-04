package proxy

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// An employee can have several accounts; each request is served by one of
// them. A conversation stays on the account it started on (its prompt cache
// and response ids live there) for as long as that account can serve it;
// otherwise the account with the most quota left is used, so an account that
// runs out hands over to the next.

// need is what a request asks of an account.
type need struct {
	model   string         // requested model without prefix; "" for any
	kind    string         // client kind
	chatgpt bool           // only a ChatGPT account will do (images, search, WebSocket)
	session string         // conversation key, to stay on one account
	exclude map[int64]bool // accounts this request already gave up on
	prefer  int64          // account to keep if it can serve (a WebSocket's)
}

// Why an account cannot serve a request, most promising first: the refusal
// shown is the one for the account closest to being usable.
const (
	blockRateLimited = iota
	blockNeedsLogin
	blockModel
	blockClient
	blockDisabled
	blockUnsupported
)

// usable reports why acc cannot serve n (ok when it can).
func usable(acc store.Account, n need, set settings.Settings, now time.Time) (block int, ok bool) {
	switch {
	case n.chatgpt && !acc.IsChatGPT():
		return blockUnsupported, false
	case acc.Status != "active":
		return blockDisabled, false
	case n.kind != KindCodex && !set.AllowNonCodex && !acc.AllowThirdParty:
		return blockClient, false
	case !acc.AllowsModel(n.model):
		return blockModel, false
	case acc.NeedsLogin:
		return blockNeedsLogin, false
	case acc.RateLimitedUntil != nil && acc.RateLimitedUntil.After(now):
		return blockRateLimited, false
	}
	return 0, true
}

// pickAccount chooses the account for a request, or the refusal to give.
// unsupported is the refusal for an endpoint no account of the employee can
// serve at all (nil: the generic one).
func (h *Handler) pickAccount(prin store.KeyPrincipal, n need, set settings.Settings, unsupported *refusal) (store.Account, *refusal) {
	if len(prin.Accounts) == 0 {
		return store.Account{}, &refusal{status: http.StatusForbidden, typ: "invalid_request_error", code: "no_account_bound",
			msg: "你还没有分配 AI 账号，已通知管理员；分配好后会在飞书告诉你。"}
	}
	now := time.Now()
	var ok []store.Account
	best, bestBlock := store.Account{}, -1
	for _, acc := range prin.Accounts {
		if n.exclude[acc.ID] {
			continue
		}
		block, fine := usable(acc, n, set, now)
		if fine {
			ok = append(ok, acc)
			continue
		}
		if bestBlock == -1 || block < bestBlock ||
			(block == blockRateLimited && block == bestBlock && acc.RateLimitedUntil.Before(*best.RateLimitedUntil)) {
			best, bestBlock = acc, block
		}
	}
	if len(ok) > 0 {
		acc := h.choose(prin.EmployeeID, ok, n)
		return acc, nil
	}
	no := func(status int, code, msg string) *refusal {
		return &refusal{status: status, typ: "invalid_request_error", code: code, msg: msg}
	}
	switch bestBlock {
	case blockRateLimited:
		return best, h.usageLimited(*best.RateLimitedUntil)
	case blockNeedsLogin:
		return best, no(http.StatusServiceUnavailable, "account_needs_login",
			"你的 AI 账号登录已失效，管理员已收到通知，重新登录后即可继续使用。")
	case blockModel:
		return best, no(http.StatusForbidden, "model_not_allowed", modelNotAllowed(n.model, prin.Accounts))
	case blockClient:
		return best, no(http.StatusForbidden, "client_not_allowed",
			"你的 AI 账号只能在官方 Codex 客户端里使用。需要用 OpenCode、Command Code 等工具，请联系管理员开通。")
	case blockDisabled:
		return best, no(http.StatusForbidden, "account_disabled", "你的 AI 账号已被管理员停用，请联系管理员。")
	}
	if unsupported != nil {
		return best, unsupported
	}
	return best, no(http.StatusForbidden, "account_unsupported", "你的 AI 账号不支持这个功能。")
}

// modelNotAllowed explains which models the employee may use instead.
func modelNotAllowed(model string, accounts []store.Account) string {
	seen := map[string]bool{}
	var allowed []string
	all := false
	for _, a := range accounts {
		if a.AllowedModels == nil {
			all = true
			break
		}
		for _, m := range a.AllowedModels {
			if !seen[m] {
				seen[m] = true
				allowed = append(allowed, m)
			}
		}
	}
	msg := "管理员没有为你的 AI 账号开放 " + model + " 模型。"
	if !all && len(allowed) > 0 {
		sort.Strings(allowed)
		msg += "可以用的模型：" + strings.Join(allowed, "、") + "。在 Codex 里输入 /model 切换。"
	} else {
		msg += "请在 Codex 里输入 /model 换一个模型。"
	}
	return msg
}

// choose picks among usable accounts: the conversation's account if it is
// one of them, else the one with the most quota left, then the least busy.
func (h *Handler) choose(empID int64, ok []store.Account, n need) store.Account {
	if len(ok) == 1 {
		h.sticky.put(empID, n.session, ok[0].ID)
		return ok[0]
	}
	if n.prefer != 0 {
		for _, a := range ok {
			if a.ID == n.prefer {
				return a
			}
		}
	}
	if id, found := h.sticky.get(empID, n.session); found {
		for _, a := range ok {
			if a.ID == id {
				return a
			}
		}
	}
	best := ok[0]
	bestUsed, _ := h.load.used(best.ID)
	bestBusy := h.busy(best.ID)
	for _, a := range ok[1:] {
		used, _ := h.load.used(a.ID)
		busy := h.busy(a.ID)
		if used < bestUsed-5 || (used <= bestUsed+5 && busy < bestBusy) {
			best, bestUsed, bestBusy = a, used, busy
		}
	}
	h.sticky.put(empID, n.session, best.ID)
	return best
}

func (h *Handler) busy(accountID int64) int {
	if h.deps.Limiter == nil {
		return 0
	}
	in, queued := h.deps.Limiter.InFlight(accountID)
	return in + queued
}

// stickyMap remembers which account each conversation used.
type stickyMap struct {
	mu sync.Mutex
	m  map[string]stickyEntry
}

type stickyEntry struct {
	account int64
	at      time.Time
}

const (
	stickyTTL = 12 * time.Hour
	stickyMax = 20000
)

func stickyKey(empID int64, session string) string {
	return strconv.FormatInt(empID, 10) + "|" + session
}

func (s *stickyMap) get(empID int64, session string) (int64, bool) {
	if session == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[stickyKey(empID, session)]
	if !ok || time.Since(e.at) > stickyTTL {
		return 0, false
	}
	return e.account, true
}

func (s *stickyMap) put(empID int64, session string, account int64) {
	if session == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]stickyEntry{}
	}
	if len(s.m) >= stickyMax {
		for k, e := range s.m {
			if time.Since(e.at) > stickyTTL || len(s.m) >= stickyMax {
				delete(s.m, k)
			}
		}
	}
	s.m[stickyKey(empID, session)] = stickyEntry{account: account, at: time.Now()}
}

// loadMap keeps each account's latest used percentage, from response headers.
type loadMap struct {
	mu sync.Mutex
	m  map[int64]float64
}

func (l *loadMap) set(accountID int64, pct float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[int64]float64{}
	}
	l.m[accountID] = pct
}

func (l *loadMap) used(accountID int64) (float64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.m[accountID]
	return v, ok
}
