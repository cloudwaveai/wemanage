package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/workdigest"
)

// Briefings and preferences belong to the current super admin.
func (s *Server) handleAdminBriefing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := currentEmployee(r)
	pref, _, err := s.deps.Store.GetAdminBriefingPref(ctx, me.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := map[string]any{
		"enabled": pref.Enabled, "prompt": pref.Prompt,
		"default_prompt": workdigest.DefaultBriefingPrompt, "running": false, "briefing": nil,
	}
	set := s.deps.Settings.Get(ctx)
	out["configured"] = set.WorkDigestAccountID != 0
	if set.WorkDigestAccountID != 0 {
		model := set.WorkDigestModel
		if model == "" {
			model = set.DefaultCodexModel
		}
		out["model"] = model
		if acc, err := s.deps.Store.GetAccount(ctx, set.WorkDigestAccountID); err == nil {
			out["account"] = map[string]any{"id": acc.ID, "email": acc.Email, "plan_type": acc.PlanType}
		}
	}
	if s.deps.Digest == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	from, to, err := s.briefingRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out["from"] = from.Format(time.DateOnly)
	out["to"] = to.AddDate(0, 0, -1).Format(time.DateOnly)
	out["today"] = time.Now().In(s.loc()).Format(time.DateOnly)
	out["running"] = s.deps.Digest.BriefingRunning(me.ID)
	days, err := s.deps.Store.WorkDigestDaysInRange(ctx, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	n := 0
	for _, d := range days {
		if d.Status == "done" {
			n++
		}
	}
	out["digest_days"] = n
	rangeRunning := s.deps.Digest.BriefingRangeRunning(me.ID, from, to)
	row, err := s.deps.Store.GetAdminBriefing(ctx, me.ID, from, to)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if err == nil {
		if row.Status == "running" {
			if rangeRunning || s.deps.Digest.BriefingRangeRunning(me.ID, from, to) {
				out["running"] = true
			} else {
				row.Status, row.Error = "failed", "上次生成被中断了（服务重启），请重新生成。"
			}
		}
		out["briefing"] = row
	}
	hist, err := s.deps.Store.RecentAdminBriefings(ctx, me.ID, 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	list := make([]map[string]any, 0, len(hist))
	for _, h := range hist {
		status := h.Status
		if status == "running" && !s.deps.Digest.BriefingRangeRunning(me.ID, h.WeekStart, h.PeriodEnd) {
			status = "failed"
		}
		list = append(list, map[string]any{
			"from": h.WeekStart.Format(time.DateOnly), "to": h.PeriodEnd.AddDate(0, 0, -1).Format(time.DateOnly),
			"status": status, "generated_at": h.GeneratedAt, "sent_at": h.SentAt,
		})
	}
	out["history"] = list
	writeJSON(w, http.StatusOK, out)
}

// briefingRange accepts inclusive calendar dates in the company timezone.
func (s *Server) briefingRange(fromText, toText string) (time.Time, time.Time, error) {
	today := midnight(time.Now(), s.loc())
	if fromText == "" && toText == "" {
		from, to := s.deps.Digest.WeekBriefingWindow(time.Now())
		if !to.After(from) {
			from = to.AddDate(0, 0, -7)
		}
		return from, to, nil
	}
	from, err := time.ParseInLocation(time.DateOnly, fromText, s.loc())
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("请选择有效的开始日期（YYYY-MM-DD）")
	}
	last, err := time.ParseInLocation(time.DateOnly, toText, s.loc())
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("请选择有效的结束日期（YYYY-MM-DD）")
	}
	if last.Before(from) {
		return time.Time{}, time.Time{}, errors.New("结束日期不能早于开始日期")
	}
	if last.After(today) {
		return time.Time{}, time.Time{}, errors.New("结束日期不能晚于今天")
	}
	to := last.AddDate(0, 0, 1)
	if to.After(from.AddDate(0, 0, 31)) {
		return time.Time{}, time.Time{}, errors.New("每份简报最多选择 31 天")
	}
	return from, to, nil
}

func (s *Server) handlePutAdminBriefing(w http.ResponseWriter, r *http.Request) {
	me := currentEmployee(r)
	var in struct {
		Enabled bool   `json:"enabled"`
		Prompt  string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if len([]rune(in.Prompt)) > 10_000 {
		writeErr(w, http.StatusBadRequest, "提示词太长了（1 万字以内）")
		return
	}
	if err := s.deps.Store.PutAdminBriefingPref(r.Context(), me.ID, in.Enabled, in.Prompt); err != nil {
		writeErr(w, http.StatusInternalServerError, "save failed")
		return
	}
	s.audit(r, "briefing_pref", "employee", strconv.FormatInt(me.ID, 10), map[string]any{"enabled": in.Enabled})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePreviewAdminBriefing(w http.ResponseWriter, r *http.Request) {
	s.startBriefing(w, r, false)
}

func (s *Server) handleSendAdminBriefing(w http.ResponseWriter, r *http.Request) {
	s.startBriefing(w, r, true)
}

func (s *Server) startBriefing(w http.ResponseWriter, r *http.Request, send bool) {
	if s.deps.Digest == nil {
		writeErr(w, http.StatusServiceUnavailable, "简报不可用")
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
	}
	if len([]rune(in.Prompt)) > 10_000 {
		writeErr(w, http.StatusBadRequest, "提示词太长了（1 万字以内）")
		return
	}
	from, to, err := s.briefingRange(in.From, in.To)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	me := currentEmployee(r)
	if send && strings.TrimSpace(in.Prompt) != "" {
		pref, _, err := s.deps.Store.GetAdminBriefingPref(r.Context(), me.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "query failed")
			return
		}
		if err := s.deps.Store.PutAdminBriefingPref(r.Context(), me.ID, pref.Enabled, in.Prompt); err != nil {
			writeErr(w, http.StatusInternalServerError, "save failed")
			return
		}
	}
	if err := s.deps.Digest.StartBriefing(r.Context(), *me, in.Prompt, send, from, to); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, workdigest.ErrBriefingBusy) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	action := "briefing_preview"
	if send {
		action = "briefing_send"
	}
	s.audit(r, action, "employee", strconv.FormatInt(me.ID, 10), map[string]any{
		"from": from.Format(time.DateOnly), "to": to.AddDate(0, 0, -1).Format(time.DateOnly),
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
