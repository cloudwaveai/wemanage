package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/health"
	"aiseat/internal/recorder"
	"aiseat/internal/risk"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The official Codex client talks to OpenAI over the Responses WebSocket
// transport (its built-in provider sets supports_websockets = true); HTTP is
// its fallback. To look like it upstream, the gateway relays Codex's
// WebSocket to CPA, which then uses a WebSocket to OpenAI too (the account's
// "websockets" flag must be on). Each `response.create` on the connection is
// treated like one HTTP request: gated, audited, counted against the
// account's concurrency limit, rewritten to the account prefix and logged.

const (
	wsReadLimit    = 64 << 20
	wsPingInterval = 30 * time.Second
	wsWriteWait    = 10 * time.Second
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	// Bearer-authenticated API clients, not browsers.
	CheckOrigin: func(*http.Request) bool { return true },
}

var wsDialer = &websocket.Dialer{
	HandshakeTimeout: 20 * time.Second,
	ReadBufferSize:   32 << 10,
	WriteBufferSize:  32 << 10,
}

// wsHandshakeHeaders are set by the WebSocket libraries themselves.
var wsHandshakeHeaders = []string{
	"Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Extensions",
	"Sec-Websocket-Accept", "Sec-Websocket-Protocol", "Upgrade", "Connection",
	"Content-Type", "Content-Length",
}

func isWebsocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, v := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(v), "upgrade") {
			return true
		}
	}
	return false
}

func wsURL(httpBase string) string {
	switch {
	case strings.HasPrefix(httpBase, "https://"):
		return "wss://" + strings.TrimPrefix(httpBase, "https://")
	case strings.HasPrefix(httpBase, "http://"):
		return "ws://" + strings.TrimPrefix(httpBase, "http://")
	}
	return httpBase
}

// handleResponsesWS serves GET /v1/responses with a WebSocket upgrade.
func (h *Handler) handleResponsesWS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	set := h.deps.Settings.Get(ctx)
	if !isWebsocketUpgrade(r) {
		oaError(w, http.StatusBadRequest, "websocket upgrade required", "invalid_request_error", "websocket_required")
		return
	}
	token := bearer(r)
	prin, acc, ge := h.authorize(ctx, token, set)
	if ge != nil {
		ge.write(w)
		return
	}
	// 426 makes Codex switch to HTTP for the rest of the session.
	if set.DisableWebsocket || !acc.IsChatGPT() {
		oaError(w, http.StatusUpgradeRequired, "websocket transport not enabled; use HTTP", "invalid_request_error", "websocket_unsupported")
		return
	}

	kind := h.classifier(set).Classify(r.Header)
	clientIP := risk.ClientIP(r, h.deps.TrustProxy)
	base := risk.Observation{EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: clientIP,
		ClientKind: kind, UserAgent: r.Header.Get("User-Agent")}
	if d := risk.NetworkGate(set, prin.RemoteAllowed, clientIP, &h.office); !d.Allow {
		o := base
		o.At, o.Denied = time.Now(), d.Code
		h.observe(o)
		oaError(w, d.Status, d.Message, "invalid_request_error", d.Code)
		return
	}
	// The installation id arrives with each response.create; checked per turn.
	if d := risk.ClientGate(set, kind, risk.DeviceInfo{InstallID: "-"}); !d.Allow {
		o := base
		o.At, o.Denied = time.Now(), d.Code
		h.observe(o)
		oaError(w, d.Status, d.Message, "invalid_request_error", d.Code)
		return
	}
	if kind == KindCodex {
		go h.recordObserved(r.Header)
	}

	upHdr := BuildUpstreamHeaders(HeaderInput{
		Kind: kind, Client: r.Header, Profile: set.CodexProfile, Device: acc.DeviceProfile,
		SessionID: SessionUUID(strconv.FormatInt(prin.EmployeeID, 10) + "|ws|" + r.Header.Get("Session_id")),
		CPAAPIKey: h.deps.CPAAPIKey,
	})
	for _, k := range wsHandshakeHeaders {
		upHdr.Del(k)
	}
	upstream, resp, err := wsDialer.DialContext(ctx, wsURL(h.deps.CPABaseURL)+"/v1/responses", upHdr)
	if err != nil {
		status := http.StatusBadGateway
		if resp != nil && resp.StatusCode >= 400 {
			status = resp.StatusCode // e.g. 426 → Codex falls back to HTTP
		}
		h.deps.Log.Warn("ws: upstream dial failed", "err", err, "status", status)
		oaError(w, status, "upstream websocket unavailable", "server_error", "upstream_unreachable")
		return
	}
	if health.HasQuotaHeaders(resp.Header) && h.quotaDue(acc.ID, 0) {
		go func(hdr http.Header) {
			_ = health.RecordQuotaFromHeaders(context.Background(), h.deps.Store, acc.ID, hdr, time.Now())
		}(resp.Header.Clone())
	}

	// Hand CPA's handshake response headers (model, reasoning flags, …) on.
	respHdr := http.Header{}
	for k, vs := range resp.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Upgrade", "Connection", "Sec-Websocket-Accept", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol":
			continue
		}
		respHdr[k] = vs
	}
	client, err := wsUpgrader.Upgrade(w, r, respHdr)
	if err != nil {
		_ = upstream.Close()
		return
	}

	s := &wsSession{
		h: h, prin: prin, acc: acc, keyHash: secure.HashAPIKey(token), token: token, kind: kind,
		ip: clientIP, hdr: r.Header.Clone(), client: client, upstream: upstream, done: make(chan struct{}),
	}
	h.trackWS(s, true)
	defer h.trackWS(s, false)
	s.run()
}

// wsTurn is one response.create in flight.
type wsTurn struct {
	rec     recorder.Record
	parser  *capture.StreamParser
	release func()
	model   string
	logIt   bool
}

type wsSession struct {
	h        *Handler
	prin     store.KeyPrincipal
	acc      store.Account
	keyHash  []byte
	token    string
	kind     string
	ip       string
	hdr      http.Header
	client   *websocket.Conn
	upstream *websocket.Conn

	cwMu  sync.Mutex // serialises writes to the client
	mu    sync.Mutex
	turns []*wsTurn
	done  chan struct{}
	once  sync.Once
}

func (s *wsSession) run() {
	s.client.SetReadLimit(wsReadLimit)
	s.upstream.SetReadLimit(wsReadLimit)

	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		s.pumpUpstream()
	}()
	go s.keepAlive()
	s.pumpClient()
	s.shutdown(websocket.CloseNormalClosure, "")
	<-upDone
	s.failPending("websocket_closed")
}

// shutdown closes both sides once.
func (s *wsSession) shutdown(code int, reason string) {
	s.once.Do(func() {
		close(s.done)
		msg := websocket.FormatCloseMessage(code, reason)
		deadline := time.Now().Add(wsWriteWait)
		s.cwMu.Lock()
		_ = s.client.WriteControl(websocket.CloseMessage, msg, deadline)
		s.cwMu.Unlock()
		_ = s.upstream.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		_ = s.client.Close()
		_ = s.upstream.Close()
	})
}

func (s *wsSession) keepAlive() {
	t := time.NewTicker(wsPingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.cwMu.Lock()
			err := s.client.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait))
			s.cwMu.Unlock()
			if err != nil {
				s.shutdown(websocket.CloseGoingAway, "")
				return
			}
		}
	}
}

func (s *wsSession) writeClient(mt int, data []byte) error {
	s.cwMu.Lock()
	defer s.cwMu.Unlock()
	_ = s.client.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return s.client.WriteMessage(mt, data)
}

// sendError reports a per-turn rejection as a Responses error event.
func (s *wsSession) sendError(status int, typ, code, msg string) {
	b, _ := json.Marshal(map[string]any{
		"type": "error", "status": status,
		"error": map[string]any{"type": typ, "code": code, "message": msg},
	})
	_ = s.writeClient(websocket.TextMessage, b)
}

// pumpClient forwards client frames upstream, turning each response.create
// into a gated, logged turn.
func (s *wsSession) pumpClient() {
	for {
		mt, data, err := s.client.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			switch gjson.GetBytes(data, "type").String() {
			case "response.create", "response.append":
				out, ok, fatal := s.beginTurn(data)
				if fatal {
					return
				}
				if !ok {
					continue
				}
				data = out
			}
		}
		if err := s.upstream.WriteMessage(mt, data); err != nil {
			return
		}
	}
}

// pumpUpstream forwards upstream frames to the client and tracks turn outcomes.
func (s *wsSession) pumpUpstream() {
	for {
		mt, data, err := s.upstream.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) && ce.Code != websocket.CloseNoStatusReceived && ce.Code != websocket.CloseAbnormalClosure {
				s.shutdown(ce.Code, ce.Text) // mirror e.g. 1009 so Codex can react
			} else {
				s.shutdown(websocket.CloseGoingAway, "upstream closed")
			}
			return
		}
		// Account for the event first (log, release the slot), then forward:
		// when the client sees a turn complete, its record is already queued.
		if mt == websocket.TextMessage {
			s.onUpstreamEvent(data)
		}
		if err := s.writeClient(mt, data); err != nil {
			s.shutdown(websocket.CloseGoingAway, "")
			return
		}
	}
}

func (s *wsSession) beginTurn(data []byte) (out []byte, ok, fatal bool) {
	h := s.h
	ctx := context.Background()
	start := time.Now()
	set := h.deps.Settings.Get(ctx)

	// Freezes, revocations and account changes apply mid-connection too.
	prin, acc, ge := h.authorize(ctx, s.token, set)
	if ge != nil {
		s.sendError(ge.status, ge.typ, ge.code, ge.msg)
		return nil, false, true
	}
	// The bound account cannot change under an open connection's prefix.
	if acc.ID != s.acc.ID {
		s.sendError(http.StatusConflict, "invalid_request_error", "account_changed", "your upstream account changed; reconnect")
		return nil, false, true
	}

	devInfo := risk.ExtractDevice(s.hdr, data)
	obs := risk.Observation{EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: s.ip, At: start,
		ClientKind: s.kind, UserAgent: s.hdr.Get("User-Agent")}
	deny := func(d risk.Decision) (out []byte, ok, fatal bool) {
		obs.Denied = d.Code
		h.observe(obs)
		s.sendError(d.Status, "invalid_request_error", d.Code, d.Message)
		return nil, false, false
	}
	if d := risk.ClientGate(set, s.kind, devInfo); !d.Allow {
		return deny(d)
	}
	if hit, flagged := h.auditor.Check(set, "responses", data); flagged {
		obs.ContentRule, obs.ContentSnippet = hit.Rule, hit.Snippet
		obs.ContentBlocked = set.ContentAuditMode == settings.AuditBlock
	}
	var deviceID *int64
	if h.deps.Risk != nil {
		dev, created, err := h.deps.Risk.Device(ctx, prin.EmployeeID, devInfo, s.kind, s.ip)
		if err != nil {
			h.deps.Log.Warn("risk: device lookup failed", "employee", prin.EmployeeID, "err", err)
		} else {
			deviceID = &dev.ID
			obs.Device, obs.NewDevice = dev, created
			if d := risk.DeviceGate(set, dev, created, time.Now(), h.deps.Risk.PortalDevicesURL()); !d.Allow {
				return deny(d)
			}
		}
	}
	if obs.ContentBlocked {
		return deny(risk.Decision{Status: http.StatusForbidden, Code: "content_blocked",
			Message: "request blocked by the company's AI usage policy (" + obs.ContentRule + "); contact the administrator if this is a mistake"})
	}
	h.observe(obs)

	capN := set.DefaultMaxConcurrency
	if acc.MaxConcurrency != nil && *acc.MaxConcurrency > 0 {
		capN = *acc.MaxConcurrency
	}
	release, waited, err := h.deps.Limiter.Acquire(ctx, acc.ID, capN, time.Duration(set.QueueTimeoutSeconds)*time.Second)
	if err != nil {
		s.sendError(http.StatusTooManyRequests, "rate_limit_error", "gateway_busy", "gateway concurrency limit reached, retry shortly")
		return nil, false, false
	}

	model := stripPrefix(gjson.GetBytes(data, "model").String())
	out = data
	if model != "" {
		out, _ = sjson.SetBytes(data, "model", acc.Prefix()+"/"+model)
	}
	reasoning := gjson.GetBytes(data, "reasoning.effort").String()
	t := &wsTurn{
		release: release, model: model,
		// generate:false is Codex's connection warm-up: no output, not worth a log row.
		logIt:  gjson.GetBytes(data, "generate").Type != gjson.False,
		parser: capture.NewStreamParser("responses", start),
		rec: recorder.Record{
			RequestID: uuid.New(), StartedAt: start, EmployeeID: prin.EmployeeID,
			AccountID: &acc.ID, APIKeyID: &prin.KeyID, SessionKey: h.sessionSeed(s.kind, s.hdr, data, prin.EmployeeID),
			ClientKind: s.kind, UserAgent: s.hdr.Get("User-Agent"), Endpoint: "responses", Stream: true,
			ModelRequested: model, ReasoningEffort: reasoning, QueuedMS: int(waited.Milliseconds()),
			ReqBody: data, ClientIP: s.ip, DeviceID: deviceID,
			Delta: strings.TrimSpace(gjson.GetBytes(data, "previous_response_id").String()) != "",
		},
	}
	s.mu.Lock()
	s.turns = append(s.turns, t)
	s.mu.Unlock()
	return out, true, false
}

func (s *wsSession) onUpstreamEvent(data []byte) {
	typ := gjson.GetBytes(data, "type").String()
	if typ == "codex.rate_limits" {
		s.recordRateLimits(data)
		return
	}
	s.mu.Lock()
	var t *wsTurn
	if len(s.turns) > 0 {
		t = s.turns[0]
	}
	s.mu.Unlock()
	if t == nil {
		return
	}
	_, _ = t.parser.Write(append(append([]byte("data: "), data...), '\n', '\n'))

	switch typ {
	case "response.completed", "response.incomplete":
		s.finishTurn(http.StatusOK, "")
	case "response.failed", "error":
		status := int(gjson.GetBytes(data, "status").Int())
		if status == 0 {
			status = http.StatusBadGateway
		}
		code := gjson.GetBytes(data, "error.code").String()
		if code == "" {
			code = gjson.GetBytes(data, "response.error.code").String()
		}
		if code == "" {
			code = typ
		}
		s.finishTurn(status, code)
	}
}

// finishTurn completes the oldest in-flight turn.
func (s *wsSession) finishTurn(status int, errType string) {
	s.mu.Lock()
	if len(s.turns) == 0 {
		s.mu.Unlock()
		return
	}
	t := s.turns[0]
	s.turns = s.turns[1:]
	s.mu.Unlock()
	s.complete(t, status, errType)
}

func (s *wsSession) failPending(errType string) {
	s.mu.Lock()
	pending := s.turns
	s.turns = nil
	s.mu.Unlock()
	for _, t := range pending {
		s.complete(t, 499, errType)
	}
}

func (s *wsSession) complete(t *wsTurn, status int, errType string) {
	h := s.h
	t.release()
	outcome := t.parser.Finish()
	t.rec.StatusCode = status
	t.rec.DurationMS = int(time.Since(t.rec.StartedAt).Milliseconds())
	t.rec.Outcome = outcome
	t.rec.ErrorType = firstNonEmpty(errType, outcome.ErrorType)
	if t.logIt {
		h.deps.Recorder.Submit(t.rec)
	}
	ctx := context.Background()
	if strings.Contains(errType, "rate_limit") || strings.Contains(errType, "usage_limit") || status == http.StatusTooManyRequests {
		until := time.Now().Add(5 * time.Minute)
		_ = h.deps.Store.SetRateLimitedUntil(ctx, s.acc.ID, &until)
		h.cache.forgetAccount(s.acc.ID)
		_, _ = h.deps.Store.AddSignal(ctx, s.acc.ID, "rate_limited", map[string]any{"until": until, "via": "websocket"})
	}
	if s.acc.IsChatGPT() && outcome.Model != "" && t.model != "" && stripPrefix(outcome.Model) != t.model {
		_, _ = h.deps.Store.AddSignal(ctx, s.acc.ID, "model_mismatch",
			map[string]any{"requested": t.model, "responded": outcome.Model})
	}
}

// recordRateLimits stores a codex.rate_limits event as a quota snapshot.
func (s *wsSession) recordRateLimits(data []byte) {
	if !s.h.quotaDue(s.acc.ID, 0) {
		return
	}
	hdr := http.Header{}
	set := func(name string, v gjson.Result) {
		if v.Exists() && v.Type != gjson.Null {
			hdr.Set(name, v.String())
		}
	}
	rl := gjson.GetBytes(data, "rate_limits")
	set("X-Codex-Primary-Used-Percent", rl.Get("primary.used_percent"))
	set("X-Codex-Primary-Window-Minutes", rl.Get("primary.window_minutes"))
	set("X-Codex-Primary-Reset-At", rl.Get("primary.reset_at"))
	set("X-Codex-Secondary-Used-Percent", rl.Get("secondary.used_percent"))
	set("X-Codex-Secondary-Window-Minutes", rl.Get("secondary.window_minutes"))
	set("X-Codex-Secondary-Reset-At", rl.Get("secondary.reset_at"))
	accID := s.acc.ID
	go func() {
		_ = health.RecordQuotaFromHeaders(context.Background(), s.h.deps.Store, accID, hdr, time.Now())
	}()
}

// trackWS registers live sessions so shutdown can close them (hijacked
// connections are not closed by http.Server.Shutdown).
func (h *Handler) trackWS(s *wsSession, add bool) {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	if h.wsSessions == nil {
		h.wsSessions = map[*wsSession]bool{}
	}
	if add {
		h.wsSessions[s] = true
	} else {
		delete(h.wsSessions, s)
	}
}

// CloseWebsockets asks every live WebSocket session to go away (server
// shutdown). Codex reconnects, or falls back to HTTP, on its own.
func (h *Handler) CloseWebsockets() {
	h.wsMu.Lock()
	sessions := make([]*wsSession, 0, len(h.wsSessions))
	for s := range h.wsSessions {
		sessions = append(sessions, s)
	}
	h.wsMu.Unlock()
	for _, s := range sessions {
		s.shutdown(websocket.CloseGoingAway, "server restarting")
	}
}
