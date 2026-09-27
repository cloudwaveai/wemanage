package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// HandlerDeps wires the data-plane dependencies.
type HandlerDeps struct {
	Store      *store.Store
	Settings   *settings.Service
	Limiter    *Limiter
	Recorder   *recorder.Recorder
	CPABaseURL string
	CPAAPIKey  string
	Upstream   *http.Client // no client timeout; per-request context carries deadlines
	Log        *slog.Logger
	// Risk registers devices and evaluates key-sharing rules; nil disables
	// device gating (client-type and freeze checks still apply).
	Risk *risk.Engine
	// TrustProxy trusts X-Forwarded-For from private-network peers (Caddy).
	TrustProxy bool
}

// Handler implements the OpenAI-compatible data plane.
type Handler struct {
	deps  HandlerDeps
	cache *principalCache

	clsMu    sync.Mutex
	cls      *Classifier
	clsRules []settings.ClientRule
}

// NewHandler builds a data-plane handler.
func NewHandler(deps HandlerDeps) *Handler {
	if deps.Upstream == nil {
		deps.Upstream = &http.Client{}
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Handler{deps: deps, cache: newPrincipalCache(deps.Store, 3*time.Second)}
}

// Routes mounts the data-plane routes.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/v1/responses", h.handleProxy("responses"))
	r.Post("/v1/responses/compact", h.handleProxy("responses"))
	r.Post("/v1/chat/completions", h.handleProxy("chat"))
	r.Get("/v1/models", h.handleModels)
}

func (h *Handler) classifier(set settings.Settings) *Classifier {
	h.clsMu.Lock()
	defer h.clsMu.Unlock()
	if h.cls != nil && sameRules(h.clsRules, set.ClientRules) {
		return h.cls
	}
	cls, err := NewClassifier(set.ClientRules)
	if err != nil {
		// fall back to a permissive classifier that treats everything as "other"
		cls, _ = NewClassifier(nil)
	}
	h.cls = cls
	h.clsRules = set.ClientRules
	return cls
}

func sameRules(a, b []settings.ClientRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// oaError writes an OpenAI-shaped error body.
func oaError(w http.ResponseWriter, status int, msg, typ, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": typ, "code": code},
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func stripPrefix(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

func (h *Handler) handleProxy(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		set := h.deps.Settings.Get(ctx)

		// Reject protocol upgrades (WebSocket) — V1 is HTTP/SSE only.
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			oaError(w, http.StatusUpgradeRequired, "websocket transport not supported", "invalid_request_error", "websocket_unsupported")
			return
		}

		// --- 1. Authenticate ---
		token := bearer(r)
		if token == "" {
			oaError(w, http.StatusUnauthorized, "missing API key", "invalid_request_error", "missing_key")
			return
		}
		prin, err := h.cache.Get(ctx, secure.HashAPIKey(token))
		if err != nil {
			oaError(w, http.StatusUnauthorized, "invalid API key", "invalid_request_error", "invalid_key")
			return
		}
		if prin.EmployeeStatus != "active" {
			oaError(w, http.StatusForbidden, "account disabled", "invalid_request_error", "employee_disabled")
			return
		}
		if prin.Frozen {
			oaError(w, http.StatusForbidden, "this key is suspended ("+prin.FrozenReason+"); contact the administrator", "invalid_request_error", "account_frozen")
			return
		}
		if prin.NoticeAckVersion < set.NoticeVersion {
			oaError(w, http.StatusForbidden, "usage notice not acknowledged; sign in to the portal first", "invalid_request_error", "notice_required")
			return
		}
		if prin.Account == nil {
			oaError(w, http.StatusForbidden, "no upstream account bound to this key", "invalid_request_error", "no_account_bound")
			return
		}
		acc := *prin.Account
		if acc.Status != "active" {
			oaError(w, http.StatusForbidden, "upstream account disabled", "invalid_request_error", "account_disabled")
			return
		}
		if acc.NeedsLogin {
			oaError(w, http.StatusServiceUnavailable, "account needs re-login; the administrator has been notified", "server_error", "account_needs_login")
			return
		}
		if acc.RateLimitedUntil != nil && acc.RateLimitedUntil.After(time.Now()) {
			retry := int(time.Until(*acc.RateLimitedUntil).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			oaError(w, http.StatusTooManyRequests, "account rate limited", "rate_limit_error", "account_rate_limited")
			return
		}

		// --- 2. Read body & classify ---
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			oaError(w, http.StatusBadRequest, "cannot read request body", "invalid_request_error", "bad_body")
			return
		}
		kind := h.classifier(set).Classify(r.Header)
		if kind == KindCodex {
			go h.recordObserved(r.Header)
		}

		// --- 2b. Anti key-sharing gate: client type, then device ---
		clientIP := risk.ClientIP(r, h.deps.TrustProxy)
		devInfo := risk.ExtractDevice(r.Header, body)
		obs := risk.Observation{
			EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: clientIP, At: start,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"),
		}
		if d := risk.ClientGate(set, kind, devInfo); !d.Allow {
			obs.Denied = d.Code
			h.observe(obs)
			oaError(w, d.Status, d.Message, "invalid_request_error", d.Code)
			return
		}
		var deviceID *int64
		if h.deps.Risk != nil {
			dev, created, err := h.deps.Risk.Device(ctx, prin.EmployeeID, devInfo, kind, clientIP)
			if err != nil {
				// Fail open: detection must not take the service down with the DB.
				h.deps.Log.Warn("risk: device lookup failed", "employee", prin.EmployeeID, "err", err)
			} else {
				deviceID = &dev.ID
				d := risk.DeviceGate(set, dev, created, time.Now(), h.deps.Risk.PortalDevicesURL())
				obs.Device, obs.NewDevice, obs.Denied = dev, created, d.Code
				h.observe(obs)
				if !d.Allow {
					oaError(w, d.Status, d.Message, "invalid_request_error", d.Code)
					return
				}
			}
		}

		model := stripPrefix(gjson.GetBytes(body, "model").String())
		stream := gjson.GetBytes(body, "stream").Bool()
		reasoning := gjson.GetBytes(body, "reasoning.effort").String()
		if reasoning == "" {
			reasoning = gjson.GetBytes(body, "reasoning_effort").String()
		}

		// --- 3. Concurrency gate ---
		capN := set.DefaultMaxConcurrency
		if acc.MaxConcurrency != nil && *acc.MaxConcurrency > 0 {
			capN = *acc.MaxConcurrency
		}
		release, waited, err := h.deps.Limiter.Acquire(ctx, acc.ID, capN, time.Duration(set.QueueTimeoutSeconds)*time.Second)
		if err != nil {
			if errors.Is(err, ErrQueueTimeout) {
				w.Header().Set("Retry-After", "1")
				oaError(w, http.StatusTooManyRequests, "gateway concurrency limit reached, retry shortly", "rate_limit_error", "gateway_busy")
				return
			}
			// context cancelled by client
			w.WriteHeader(499)
			return
		}
		defer release()

		// --- 4. Build upstream request ---
		sessionSeed := h.sessionSeed(kind, r.Header, body, prin.EmployeeID)
		sessionID := SessionUUID(sessionSeed)

		upstreamModel := acc.Prefix() + "/" + model
		outBody, _ := sjson.SetBytes(body, "model", upstreamModel)

		hdr := BuildUpstreamHeaders(HeaderInput{
			Kind:      kind,
			Client:    r.Header,
			Profile:   set.CodexProfile,
			Device:    acc.DeviceProfile,
			SessionID: sessionID,
			CPAAPIKey: h.deps.CPAAPIKey,
		})

		upURL := h.deps.CPABaseURL + "/v1/" + endpointPath(endpoint, r.URL.Path)
		timeout := 100 * time.Second
		if stream {
			timeout = 600 * time.Second
		}
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		upReq, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, upURL, strings.NewReader(string(outBody)))
		upReq.Header = hdr

		rec := recorder.Record{
			RequestID: uuid.New(), StartedAt: start, EmployeeID: prin.EmployeeID,
			AccountID: &acc.ID, APIKeyID: &prin.KeyID, SessionKey: sessionSeed,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"), Endpoint: endpoint,
			Stream: stream, ModelRequested: model, ReasoningEffort: reasoning,
			QueuedMS: int(waited.Milliseconds()), ReqBody: body,
			ClientIP: clientIP, DeviceID: deviceID,
		}

		resp, err := h.deps.Upstream.Do(upReq)
		if err != nil {
			rec.StatusCode = http.StatusBadGateway
			rec.ErrorType = "upstream_unreachable"
			rec.ErrorBody = err.Error()
			rec.DurationMS = int(time.Since(start).Milliseconds())
			h.deps.Recorder.Submit(rec)
			oaError(w, http.StatusBadGateway, "upstream unavailable", "server_error", "upstream_unreachable")
			return
		}
		defer resp.Body.Close()

		// --- 5. Relay response, tee into parser ---
		go func(hdr http.Header) {
			_ = health.RecordQuotaFromHeaders(context.Background(), h.deps.Store, acc.ID, hdr, time.Now())
		}(resp.Header.Clone())
		copyResponseHeaders(w, resp.Header)
		w.WriteHeader(resp.StatusCode)

		parser := capture.NewStreamParser(endpoint, start)
		var outcome capture.Outcome
		if isEventStream(resp.Header) || stream {
			tee := io.TeeReader(resp.Body, parser)
			flushCopy(w, tee)
			outcome = parser.Finish()
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			_, _ = w.Write(respBody)
			outcome = capture.ParseJSONResponse(endpoint, respBody)
		}

		rec.StatusCode = resp.StatusCode
		rec.DurationMS = int(time.Since(start).Milliseconds())
		rec.Outcome = outcome
		if resp.StatusCode >= 400 {
			rec.ErrorType = firstNonEmpty(outcome.ErrorType, "upstream_"+strconv.Itoa(resp.StatusCode))
		}
		h.deps.Recorder.Submit(rec)

		// --- 6. React to upstream status / degradation ---
		h.reactToUpstream(context.WithoutCancel(ctx), acc, resp, model, outcome, set)
	}
}

func (h *Handler) observe(o risk.Observation) {
	if h.deps.Risk != nil {
		h.deps.Risk.Observe(o)
	}
}

// sessionSeed derives a stable per-conversation seed.
func (h *Handler) sessionSeed(kind string, hdr http.Header, body []byte, empID int64) string {
	if kind == KindCodex {
		if s := hdr.Get("Session-Id"); s != "" {
			return s
		}
		if s := hdr.Get("Session_id"); s != "" {
			return s
		}
		if k := gjson.GetBytes(body, "prompt_cache_key").String(); k != "" {
			return k
		}
	}
	// Fall back to employee + first user text so a conversation is stable.
	first := gjson.GetBytes(body, "input.0").Raw
	if first == "" {
		first = gjson.GetBytes(body, "messages.0").Raw
	}
	return strconv.FormatInt(empID, 10) + "|" + hashSeed(first)
}

func (h *Handler) recordObserved(hdr http.Header) {
	ua := hdr.Get("User-Agent")
	if ua == "" {
		return
	}
	ver := ""
	if p, ok := ParseCodexUA(ua); ok {
		ver = p.Version
	}
	_ = h.deps.Store.UpsertObservedClient(context.Background(), store.ObservedClient{
		UserAgent: ua, Originator: hdr.Get("Originator"), Version: ver,
	})
}

func (h *Handler) reactToUpstream(ctx context.Context, acc store.Account, resp *http.Response, model string, outcome capture.Outcome, set settings.Settings) {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		until := parseRateLimitReset(resp.Header)
		_ = h.deps.Store.SetRateLimitedUntil(ctx, acc.ID, &until)
		_, _ = h.deps.Store.AddSignal(ctx, acc.ID, "rate_limited", map[string]any{"until": until})
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_ = h.deps.Store.SetNeedsLogin(ctx, acc.ID, true)
		_, _ = h.deps.Store.AddSignal(ctx, acc.ID, "needs_login", map[string]any{"status": resp.StatusCode})
	}
	// Model mismatch (silent degradation) signal.
	if outcome.Model != "" && model != "" && stripPrefix(outcome.Model) != model {
		_, _ = h.deps.Store.AddSignal(ctx, acc.ID, "model_mismatch",
			map[string]any{"requested": model, "responded": outcome.Model})
	}
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := bearer(r)
	if token == "" {
		oaError(w, http.StatusUnauthorized, "missing API key", "invalid_request_error", "missing_key")
		return
	}
	prin, err := h.cache.Get(ctx, secure.HashAPIKey(token))
	if err != nil || prin.Account == nil {
		oaError(w, http.StatusForbidden, "no account bound", "invalid_request_error", "no_account_bound")
		return
	}
	if prin.Frozen || prin.EmployeeStatus != "active" {
		oaError(w, http.StatusForbidden, "this key is suspended; contact the administrator", "invalid_request_error", "account_frozen")
		return
	}
	acc := *prin.Account
	prefix := acc.Prefix() + "/"

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	upReq, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, h.deps.CPABaseURL+"/v1/models", nil)
	upReq.Header.Set("Authorization", "Bearer "+h.deps.CPAAPIKey)

	resp, err := h.deps.Upstream.Do(upReq)
	if err != nil {
		writeStaticModels(w)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out []map[string]any
	gjson.GetBytes(raw, "data").ForEach(func(_, v gjson.Result) bool {
		id := v.Get("id").String()
		if strings.HasPrefix(id, prefix) {
			m := map[string]any{"id": strings.TrimPrefix(id, prefix), "object": "model", "owned_by": "company"}
			out = append(out, m)
		}
		return true
	})
	if len(out) == 0 {
		writeStaticModels(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

func writeStaticModels(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "gpt-5.1-codex", "object": "model", "owned_by": "company"},
			{"id": "gpt-5.1", "object": "model", "owned_by": "company"},
		},
	})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// endpointPath maps the logical endpoint back to the CPA path suffix.
func endpointPath(endpoint, reqPath string) string {
	if endpoint == "chat" {
		return "chat/completions"
	}
	if strings.HasSuffix(reqPath, "/compact") {
		return "responses/compact"
	}
	return "responses"
}
