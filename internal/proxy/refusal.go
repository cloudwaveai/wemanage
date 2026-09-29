package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aiseat/internal/recorder"
	"aiseat/internal/risk"
	"aiseat/internal/timefmt"

	"github.com/tidwall/gjson"
)

// A refusal is the gateway turning a request away. Its message is written for
// the employee, in Chinese, and says what to do next.
type refusal struct {
	status     int
	typ, code  string
	msg        string
	retryAfter string
	resetsAt   time.Time // usage limits: when the account can be used again
}

const typeUsageLimit = "usage_limit_reached"

// needsPerson lists refusals no retry can fix: someone has to confirm a
// device, bind an account, log in again or change a setting first.
var needsPerson = map[string]bool{
	"missing_key": true, "invalid_key": true, "employee_disabled": true, "account_frozen": true,
	"no_account_bound": true, "account_disabled": true, "account_needs_login": true,
	"network_not_allowed": true, "client_not_allowed": true, "device_blocked": true,
	"device_unconfirmed": true, "content_blocked": true, "images_unsupported": true,
}

// wire shapes a refusal for the client. Codex retries other 4xx responses
// (five times, then five more over HTTP after a WebSocket) before it shows
// their message, but shows an invalid_prompt error's message as it is and
// stops. So a refusal only a person can fix reaches Codex in that shape, with
// the gateway's own code kept in "reason". A used-up account gets Codex's
// usage-limit shape, which also stops at once and shows when it resets.
func (f *refusal) wire(kind string) (int, map[string]any) {
	e := map[string]any{"message": f.msg, "type": f.typ, "code": f.code}
	switch {
	case f.typ == typeUsageLimit:
		if !f.resetsAt.IsZero() {
			e["resets_at"] = f.resetsAt.Unix()
			e["resets_in_seconds"] = max(0, int64(time.Until(f.resetsAt).Seconds()))
		}
		return http.StatusTooManyRequests, e
	case kind == KindCodex && needsPerson[f.code]:
		e["code"], e["reason"] = "invalid_prompt", f.code
		return http.StatusBadRequest, e
	}
	return f.status, e
}

// write sends the refusal as an OpenAI-shaped error and returns the status sent.
func (f *refusal) write(w http.ResponseWriter, kind string) int {
	if f.retryAfter != "" {
		w.Header().Set("Retry-After", f.retryAfter)
	}
	status, e := f.wire(kind)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": e})
	return status
}

// event is the refusal as a Responses WebSocket error event.
func (f *refusal) event(kind string) (int, []byte) {
	status, e := f.wire(kind)
	b, _ := json.Marshal(map[string]any{"type": "error", "status": status, "error": e})
	return status, b
}

func fromDecision(d risk.Decision) *refusal {
	return &refusal{status: d.Status, typ: "invalid_request_error", code: d.Code, msg: d.Message}
}

// refuse answers with f and, when the key is known, logs the request like any
// other so the employee's usage log explains what went wrong.
func (h *Handler) refuse(w http.ResponseWriter, f *refusal, kind string, rec *recorder.Record) {
	h.logRefusal(rec, f.write(w, kind), f.code)
}

func (h *Handler) logRefusal(rec *recorder.Record, status int, code string) {
	if rec == nil || rec.EmployeeID == 0 {
		return
	}
	rec.StatusCode, rec.ErrorType = status, code
	rec.DurationMS = int(time.Since(rec.StartedAt).Milliseconds())
	if !gjson.ValidBytes(rec.ReqBody) {
		rec.ReqBody = []byte("{}")
	}
	h.deps.Recorder.Submit(*rec)
}

func (h *Handler) portal(path string) string {
	return strings.TrimRight(h.deps.PortalURL, "/") + path
}

// usageLimited is the refusal for an account whose quota is used up.
func (h *Handler) usageLimited(until time.Time) *refusal {
	msg := "这个账号的额度用完了，请稍后再试。"
	if !until.IsZero() {
		msg = "这个账号的额度用完了，预计 " + h.fmtReset(until) + " 恢复。"
	}
	return &refusal{
		status: http.StatusTooManyRequests, typ: typeUsageLimit, code: "account_rate_limited",
		msg:        msg + "额度情况可在 " + h.portal("/usage") + " 查看。",
		retryAfter: fmt.Sprint(max(1, int(time.Until(until).Seconds())+1)),
		resetsAt:   until,
	}
}

// fmtReset renders a reset time the way people say it: "15:20",
// "明天 09:10" or "10月3日 09:10".
func (h *Handler) fmtReset(t time.Time) string {
	return timefmt.Relative(t, time.Now(), h.deps.Loc)
}

func contentBlocked(rule string) risk.Decision {
	return risk.Decision{Status: http.StatusForbidden, Code: "content_blocked",
		Message: "这条请求触发了公司的 AI 使用规则（" + rule + "），已被拦截。如果是误判，请联系管理员。"}
}

// quotaReset reports when a 429 from upstream ends, if it is a used-up quota
// rather than a brief rate limit: upstream says usage_limit_reached, or the
// wait it gives (in the body or the headers) is a minute or more.
func quotaReset(body []byte, hdr http.Header, now time.Time) (time.Time, bool) {
	e := gjson.GetBytes(body, "error")
	if !e.Exists() {
		e = gjson.ParseBytes(body)
	}
	usage := e.Get("type").String() == typeUsageLimit
	until, found := time.Time{}, true
	switch {
	case e.Get("resets_at").Int() > 0:
		until = time.Unix(e.Get("resets_at").Int(), 0)
	case e.Get("resets_in_seconds").Int() > 0:
		until = now.Add(time.Duration(e.Get("resets_in_seconds").Int()) * time.Second)
	case e.Get("reset_seconds").Int() > 0: // CPA's model_cooldown
		until = now.Add(time.Duration(e.Get("reset_seconds").Int()) * time.Second)
	default:
		until, found = headerReset(hdr, now)
	}
	if !found || !until.After(now) {
		if !usage {
			return time.Time{}, false
		}
		until = now.Add(5 * time.Minute)
	}
	if !usage && until.Sub(now) < time.Minute {
		return time.Time{}, false
	}
	return until, true
}
