package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"aiseat/internal/health"
	"aiseat/internal/store"
)

// resetCreditMinPct is how much of a window must be used before an employee
// may spend a reset credit on it: a credit resets both windows to zero, so
// spending it early throws most of it away.
const resetCreditMinPct = 80

// resetAllowed reports whether the account is used enough for a reset credit
// to be worth spending, and if not, why.
func (s *Server) resetAllowed(ctx context.Context, acc store.Account) (bool, string) {
	if acc.RateLimitedUntil != nil && acc.RateLimitedUntil.After(time.Now()) {
		return true, ""
	}
	if q, err := s.deps.Store.LatestQuota(ctx, acc.ID); err == nil {
		for _, p := range []*float64{q.PrimaryUsedPct, q.SecondaryUsedPct} {
			if p != nil && *p >= resetCreditMinPct {
				return true, ""
			}
		}
	}
	return false, fmt.Sprintf("5 小时或每周额度用到 %d%% 以上（或已用完）时才能使用，避免浪费。", resetCreditMinPct)
}

// chatgptAccount is the employee's bound account when it can have reset
// credits.
func (s *Server) chatgptAccount(r *http.Request) (store.Account, bool) {
	acc, err := s.boundAccount(r.Context(), currentEmployee(r).ID)
	if err != nil || !acc.IsChatGPT() || s.deps.Quota == nil {
		return store.Account{}, false
	}
	return acc, true
}

// handleMyResetCredits lists the reset credits (重置卡) of the employee's account.
func (s *Server) handleMyResetCredits(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.chatgptAccount(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false, "available": 0, "credits": []health.ResetCredit{},
			"can_use": false, "reason": "你的 AI 账号没有重置卡。"})
		return
	}
	c, err := s.deps.Quota.ResetCredits(r.Context(), acc)
	if err != nil {
		s.deps.Log.Warn("reset credits", "account", acc.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "暂时查不到重置卡，请稍后再试")
		return
	}
	can, reason := s.resetAllowed(r.Context(), acc)
	if c.Available == 0 {
		can, reason = false, "暂时没有可用的重置卡。"
	}
	credits := c.Credits
	if credits == nil {
		credits = []health.ResetCredit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"supported": true, "available": c.Available, "credits": credits,
		"can_use": can, "reason": reason})
}

// handleUseResetCredit spends one reset credit on the employee's account.
func (s *Server) handleUseResetCredit(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.chatgptAccount(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "你的 AI 账号没有重置卡")
		return
	}
	if can, reason := s.resetAllowed(r.Context(), acc); !can {
		writeErr(w, http.StatusConflict, reason)
		return
	}
	code, err := s.deps.Quota.UseResetCredit(r.Context(), acc)
	if err != nil {
		s.deps.Log.Warn("use reset credit", "account", acc.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "重置没有成功，请稍后再试")
		return
	}
	s.audit(r, "reset_credit_use", "account", fmt.Sprint(acc.ID), map[string]any{"code": code})
	msg := map[string]string{
		health.ResetDone:           "额度已重置，5 小时和每周额度都重新开始计算。",
		health.ResetNothingToReset: "现在没有需要重置的额度，重置卡没有被使用。",
		health.ResetNoCredit:       "没有可用的重置卡了。",
		health.ResetAlreadyUsed:    "这张重置卡已经用过了。",
	}[code]
	if msg == "" {
		msg = "重置结果：" + code
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": code == health.ResetDone, "code": code, "message": msg})
}
