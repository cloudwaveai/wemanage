package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/recorder"
	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// A channel must not reach a private service, even through DNS or a redirect.
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func newChannelClient() *http.Client {
	dial := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("upstream DNS has no addresses")
			}
			for _, ip := range ips {
				if !publicAddress(ip) {
					return nil, errors.New("upstream address is not public")
				}
			}
			var last error
			for _, ip := range ips {
				c, e := dial.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
				last = e
			}
			return nil, last
		}}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// ValidChannelURL accepts a HTTPS base without embedded credentials or query.
func ValidChannelURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(raw, "\r\n")
}

func (h *Handler) forwardChannel(w http.ResponseWriter, r *http.Request, prin store.KeyPrincipal, body []byte, endpoint, model string, stream bool, start time.Time, set settings.Settings, rec *recorder.Record) {
	ctx := r.Context()
	c, err := h.deps.Store.GetAPIChannel(ctx, *prin.APIChannelID)
	if err != nil || !c.Active || !ValidChannelURL(c.BaseURL) || h.deps.Box == nil {
		h.refuse(w, &refusal{status: 503, typ: "server_error", code: "channel_unavailable", msg: "上游渠道不可用，请联系管理员。"}, rec.ClientKind, rec)
		return
	}
	suffix := ""
	switch {
	case c.Protocol == "openai" && endpoint == "chat":
		suffix = "/chat/completions"
	case c.Protocol == "openai" && endpoint == "responses" && r.URL.Path == "/v1/responses":
		suffix = "/responses"
	case c.Protocol == "anthropic" && endpoint == "messages":
		suffix = "/messages"
	case c.Protocol == "anthropic" && endpoint == "count_tokens":
		suffix = "/messages/count_tokens"
	default:
		h.refuse(w, &refusal{status: 400, typ: "invalid_request_error", code: "channel_protocol_mismatch", msg: "该密钥绑定的渠道不支持此接口。"}, rec.ClientKind, rec)
		return
	}
	release, waited, err := h.deps.Limiter.Acquire(ctx, -c.ID, set.DefaultMaxConcurrency, time.Duration(set.QueueTimeoutSeconds)*time.Second)
	if err != nil {
		h.refuse(w, gatewayBusy(), rec.ClientKind, rec)
		return
	}
	defer release()
	rec.QueuedMS += int(waited.Milliseconds())
	credential, err := h.deps.Box.Open(c.CredentialCipher)
	if err != nil {
		h.refuse(w, &refusal{status: 503, typ: "server_error", code: "channel_unavailable", msg: "上游渠道凭证不可用。"}, rec.ClientKind, rec)
		return
	}
	timeout := 100 * time.Second
	if stream {
		timeout = 600 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+suffix, strings.NewReader(string(body)))
	if err != nil {
		h.refuse(w, &refusal{status: 503, typ: "server_error", code: "channel_unavailable", msg: "上游渠道地址无效。"}, rec.ClientKind, rec)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Protocol == "anthropic" {
		req.Header.Set("x-api-key", string(credential))
		version := r.Header.Get("anthropic-version")
		if version == "" {
			version = "2023-06-01"
		}
		req.Header.Set("anthropic-version", version)
		if beta := r.Header.Get("anthropic-beta"); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+string(credential))
	}
	resp, err := h.channelHTTP.Do(req)
	if err != nil {
		rec.StatusCode = http.StatusBadGateway
		rec.ErrorType = "channel_unreachable"
		rec.DurationMS = int(time.Since(start).Milliseconds())
		h.deps.Recorder.Submit(*rec)
		oaError(w, 502, "上游渠道连接失败。", "server_error", "channel_unreachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		rec.StatusCode = 502
		rec.ErrorType = "channel_redirect"
		h.deps.Recorder.Submit(*rec)
		oaError(w, 502, "上游渠道返回重定向，请检查基础地址。", "server_error", "channel_redirect")
		return
	}
	copyChannelHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	parser := capture.NewStreamParser(endpoint, start)
	var outcome capture.Outcome
	if isEventStream(resp.Header) || (stream && resp.StatusCode < 400 && resp.Header.Get("Content-Type") == "") {
		flushCopy(w, io.TeeReader(resp.Body, parser))
		outcome = parser.Finish()
	} else {
		b, _ := io.ReadAll(resp.Body)
		_, _ = w.Write(b)
		outcome = capture.ParseJSONResponse(endpoint, b)
	}
	rec.StatusCode = resp.StatusCode
	rec.DurationMS = int(time.Since(start).Milliseconds())
	rec.Outcome = outcome
	if resp.StatusCode >= 400 {
		rec.ErrorType = "upstream_error"
	}
	h.deps.Recorder.Submit(*rec)
}

func copyChannelHeaders(w http.ResponseWriter, src http.Header) {
	for _, k := range []string{"Content-Type", "Retry-After", "X-Request-Id", "Anthropic-RateLimit-Requests-Limit", "Anthropic-RateLimit-Requests-Remaining", "X-Ratelimit-Limit-Requests", "X-Ratelimit-Remaining-Requests"} {
		for _, v := range src.Values(k) {
			w.Header().Add(k, v)
		}
	}
}
func (h *Handler) channelModels(w http.ResponseWriter, r *http.Request, prin store.KeyPrincipal) {
	c, err := h.deps.Store.GetAPIChannel(r.Context(), *prin.APIChannelID)
	if err != nil || !c.Active || !ValidChannelURL(c.BaseURL) || h.deps.Box == nil {
		oaError(w, 503, "渠道不可用。", "server_error", "channel_unavailable")
		return
	}
	credential, err := h.deps.Box.Open(c.CredentialCipher)
	if err != nil {
		oaError(w, 503, "渠道凭证不可用。", "server_error", "channel_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		oaError(w, 503, "渠道地址无效。", "server_error", "channel_unavailable")
		return
	}
	if c.Protocol == "anthropic" {
		req.Header.Set("x-api-key", string(credential))
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+string(credential))
	}
	resp, err := h.channelHTTP.Do(req)
	if err != nil {
		oaError(w, 502, "上游渠道连接失败。", "server_error", "channel_unreachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		oaError(w, 502, "上游渠道返回重定向。", "server_error", "channel_redirect")
		return
	}
	copyChannelHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
