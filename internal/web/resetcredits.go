package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"aiseat/internal/health"
	"aiseat/internal/store"
)

// chatgptAccounts are the employee's accounts that can have reset credits.
func (s *Server) chatgptAccounts(r *http.Request) []store.Account {
	if s.deps.Quota == nil {
		return nil
	}
	accs, _ := s.deps.Store.AccountsOf(r.Context(), currentEmployee(r).ID)
	var out []store.Account
	for _, a := range accs {
		if a.IsChatGPT() {
			out = append(out, a)
		}
	}
	return out
}

// handleMyResetCredits lists the reset credits (重置卡) of each of the
// employee's ChatGPT accounts.
func (s *Server) handleMyResetCredits(w http.ResponseWriter, r *http.Request) {
	accs := s.chatgptAccounts(r)
	out := make([]map[string]any, 0, len(accs))
	for _, acc := range accs {
		entry := map[string]any{"account_id": acc.ID, "email": acc.Email, "plan_type": acc.PlanType,
			"supported": true, "available": 0, "credits": []health.ResetCredit{}, "can_use": false}
		c, err := s.deps.Quota.ResetCredits(r.Context(), acc)
		if err != nil {
			s.deps.Log.Warn("reset credits", "account", acc.ID, "err", err)
			entry["error"] = "暂时查不到重置卡，请稍后再试。"
			out = append(out, entry)
			continue
		}
		if c.Credits != nil {
			entry["credits"] = c.Credits
		}
		entry["available"] = c.Available
		entry["can_use"] = c.Available > 0
		if c.Available == 0 {
			entry["reason"] = "暂时没有可用的重置卡。"
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// handleUseResetCredit spends one reset credit on one of the employee's accounts.
func (s *Server) handleUseResetCredit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	accs := s.chatgptAccounts(r)
	var acc store.Account
	for _, a := range accs {
		if a.ID == body.AccountID || (body.AccountID == 0 && len(accs) == 1) {
			acc = a
		}
	}
	if acc.ID == 0 {
		writeErr(w, http.StatusBadRequest, "你的 AI 账号没有重置卡")
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
