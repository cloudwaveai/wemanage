package proxy

import (
	"net/http"
	"testing"
	"time"
)

func TestQuotaReset(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name   string
		body   string
		hdr    http.Header
		want   time.Duration
		isUsed bool
	}{
		{"usage limit with resets_at", `{"error":{"type":"usage_limit_reached","resets_at":1800003600}}`, nil, time.Hour, true},
		{"usage limit with resets_in_seconds", `{"error":{"type":"usage_limit_reached","resets_in_seconds":600}}`, nil, 10 * time.Minute, true},
		{"usage limit without a time", `{"error":{"type":"usage_limit_reached"}}`, nil, 5 * time.Minute, true},
		{"CPA cooldown", `{"error":{"code":"model_cooldown","reset_seconds":7200}}`, nil, 2 * time.Hour, true},
		{"long reset from headers", `{"error":{"message":"x"}}`, http.Header{"Retry-After": {"3600"}}, time.Hour, true},
		{"brief rate limit", `{"error":{"type":"rate_limit_exceeded"}}`, http.Header{"Retry-After": {"5"}}, 0, false},
		{"no information", `{"error":{"message":"x"}}`, nil, 0, false},
	}
	for _, c := range cases {
		until, ok := quotaReset([]byte(c.body), c.hdr, now)
		if ok != c.isUsed || (ok && until.Sub(now) != c.want) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, until.Sub(now), ok, c.want, c.isUsed)
		}
	}
}

func TestRefusalWire(t *testing.T) {
	f := &refusal{status: http.StatusForbidden, typ: "invalid_request_error", code: "device_unconfirmed", msg: "m"}
	if st, e := f.wire(KindCodex); st != 400 || e["code"] != "invalid_prompt" || e["reason"] != "device_unconfirmed" {
		t.Fatalf("codex: %d %v", st, e)
	}
	if st, e := f.wire("other"); st != 403 || e["code"] != "device_unconfirmed" {
		t.Fatalf("other: %d %v", st, e)
	}
	busy := gatewayBusy()
	if st, e := busy.wire(KindCodex); st != 429 || e["code"] != "gateway_busy" {
		t.Fatalf("busy stays retryable: %d %v", st, e)
	}
	h := &Handler{deps: HandlerDeps{PortalURL: "https://p/"}}
	until := time.Now().Add(time.Hour)
	st, e := h.usageLimited(until).wire(KindCodex)
	if st != 429 || e["type"] != "usage_limit_reached" || e["resets_at"] != until.Unix() {
		t.Fatalf("usage limit: %d %v", st, e)
	}
}

func TestFmtReset(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	h := &Handler{deps: HandlerDeps{Loc: loc}}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 15, 20, 0, 0, loc)
	if got := h.fmtReset(today); got != "15:20" {
		t.Errorf("today: %q", got)
	}
	if got := h.fmtReset(today.AddDate(0, 0, 1)); got != "明天 15:20" {
		t.Errorf("tomorrow: %q", got)
	}
	later := today.AddDate(0, 0, 3)
	if got, want := h.fmtReset(later), later.Format("1月2日 15:04"); got != want {
		t.Errorf("later: %q, want %q", got, want)
	}
}

func TestQuotaUsedUp(t *testing.T) {
	for _, c := range []struct {
		h    map[string]string
		want bool
	}{
		{map[string]string{"Retry-After": "5"}, false},
		{map[string]string{"X-Codex-Primary-Used-Percent": "42"}, false},
		{map[string]string{"X-Codex-Secondary-Used-Percent": "100"}, true},
		{map[string]string{"X-Codex-Limit-Reached": "true"}, true},
	} {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}
		if got := quotaUsedUp(h); got != c.want {
			t.Errorf("%v: %v, want %v", c.h, got, c.want)
		}
	}
}
