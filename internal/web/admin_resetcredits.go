package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"aiseat/internal/health"
	"aiseat/internal/store"
	"github.com/go-chi/chi/v5"
)

func (s *Server) adminCreditAccount(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "账号编号无效")
		return store.Account{}, false
	}
	if s.deps.Quota == nil {
		writeErr(w, http.StatusServiceUnavailable, "额度服务暂不可用")
		return store.Account{}, false
	}
	acc, err := s.deps.Store.GetAccount(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "账号不存在")
		return store.Account{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取账号")
		return store.Account{}, false
	}
	if !acc.IsChatGPT() {
		writeErr(w, http.StatusBadRequest, "该类型账号不支持重置卡和此恢复操作")
		return store.Account{}, false
	}
	return acc, true
}

func (s *Server) handleAdminResetCredits(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.adminCreditAccount(w, r)
	if !ok {
		return
	}
	credits, err := s.deps.Quota.ResetCredits(r.Context(), acc)
	if err != nil {
		s.deps.Log.Warn("admin reset credits", "account", acc.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "暂时查不到重置卡，请稍后再试")
		return
	}
	if credits.Credits == nil {
		credits.Credits = []health.ResetCredit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_id": acc.ID, "available": credits.Available, "credits": credits.Credits})
}

func (s *Server) handleAdminUseResetCredit(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.adminCreditAccount(w, r)
	if !ok {
		return
	}
	s.useResetCredit(w, r, acc)
}

func (s *Server) handleAdminReconcileRateLimit(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.adminCreditAccount(w, r)
	if !ok {
		return
	}
	err := s.deps.Quota.ReconcileRateLimit(r.Context(), acc)
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	s.audit(r.WithContext(auditCtx), "rate_limit_reconcile", "account", fmt.Sprint(acc.ID), map[string]any{"success": err == nil})
	if errors.Is(err, health.ErrQuotaStillLimited) {
		writeErr(w, http.StatusConflict, "上游仍限流或未明确确认额度可用，不能清除限制。请查看额度或使用重置卡。")
		return
	}
	if err != nil {
		s.deps.Log.Warn("reconcile rate limit", "account", acc.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "无法完成限流状态核对，请稍后查看账号状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "上游确认额度可用，已清理该账号的旧限流状态。"})
}
