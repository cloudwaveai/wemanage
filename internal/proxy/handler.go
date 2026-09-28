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

	"aiseat/internal/audit"
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

	office  risk.OfficeMatcher
	auditor audit.Auditor

	wsMu       sync.Mutex
	wsSessions map[*wsSession]bool

	// Per-request bookkeeping writes are coalesced (the database may be remote).
	bkMu     sync.Mutex
	observed map[string]*observedEntry // User-Agent -> uses not yet written
	quotaAt  map[int64]time.Time       // account -> last quota snapshot write
}

type observedEntry struct {
	originator, version string
	pending             int64
	written             time.Time
}

const (
	observedEvery = 10 * time.Minute // a client UA is written at most this often (with its count)
	quotaEvery    = time.Minute      // an account's quota snapshot is written at most this often
)

// NewHandler builds a data-plane handler.
func NewHandler(deps HandlerDeps) *Handler {
	if deps.Upstream == nil {
		deps.Upstream = &http.Client{}
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Handler{deps: deps, cache: newPrincipalCache(deps.Store, 3*time.Second),
		observed: map[string]*observedEntry{}, quotaAt: map[int64]time.Time{}}
}

// Routes mounts the data-plane routes.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/v1/responses", h.handleProxy("responses"))
	r.Get("/v1/responses", h.handleResponsesWS) // Responses WebSocket transport
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
		prin, acc, ge := h.authorize(ctx, bearer(r), set)
		if ge != nil {
			ge.write(w)
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

		// --- 2b. Gates: office network, client type, content audit, device ---
		clientIP := risk.ClientIP(r, h.deps.TrustProxy)
		devInfo := risk.ExtractDevice(r.Header, body)
		obs := risk.Observation{
			EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: clientIP, At: start,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"),
		}
		deny := func(d risk.Decision) {
			obs.Denied = d.Code
			h.observe(obs)
			oaError(w, d.Status, d.Message, "invalid_request_error", d.Code)
		}
		if d := risk.NetworkGate(set, prin.RemoteAllowed, clientIP, &h.office); !d.Allow {
			deny(d)
			return
		}
		if d := risk.ClientGate(set, kind, devInfo); !d.Allow {
			deny(d)
			return
		}
		if hit, flagged := h.auditor.Check(set, endpoint, body); flagged {
			obs.ContentRule, obs.ContentSnippet = hit.Rule, hit.Snippet
			obs.ContentBlocked = set.ContentAuditMode == settings.AuditBlock
		}
		var deviceID *int64
		if h.deps.Risk != nil {
			dev, created, err := h.deps.Risk.Device(ctx, prin.EmployeeID, devInfo, kind, clientIP)
			if err != nil {
				// Fail open: detection must not take the service down with the DB.
				h.deps.Log.Warn("risk: device lookup failed", "employee", prin.EmployeeID, "err", err)
			} else {
				deviceID = &dev.ID
				obs.Device, obs.NewDevice = dev, created
				if d := risk.DeviceGate(set, dev, created, time.Now(), h.deps.Risk.PortalDevicesURL()); !d.Allow {
					deny(d)
					return
				}
			}
		}
		if obs.ContentBlocked {
			deny(risk.Decision{Status: http.StatusForbidden, Code: "content_blocked",
				Message: "request blocked by the company's AI usage policy (" + obs.ContentRule + "); contact the administrator if this is a mistake"})
			return
		}
		h.observe(obs)

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
		if health.HasQuotaHeaders(resp.Header) && h.quotaDue(acc.ID, resp.StatusCode) {
			go func(hdr http.Header) {
				_ = health.RecordQuotaFromHeaders(context.Background(), h.deps.Store, acc.ID, hdr, time.Now())
			}(resp.Header.Clone())
		}
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

// gateError is an OpenAI-shaped rejection produced before any upstream work.
type gateError struct {
	status     int
	msg, typ   string
	code       string
	retryAfter string
}

func (g *gateError) write(w http.ResponseWriter) {
	if g.retryAfter != "" {
		w.Header().Set("Retry-After", g.retryAfter)
	}
	oaError(w, g.status, g.msg, g.typ, g.code)
}

// authorize resolves a bearer token to its principal and bound account and
// applies the employee/notice/account checks shared by the HTTP and
// WebSocket paths.
func (h *Handler) authorize(ctx context.Context, token string, set settings.Settings) (store.KeyPrincipal, store.Account, *gateError) {
	deny := func(status int, msg, typ, code string) (store.KeyPrincipal, store.Account, *gateError) {
		return store.KeyPrincipal{}, store.Account{}, &gateError{status: status, msg: msg, typ: typ, code: code}
	}
	if token == "" {
		return deny(http.StatusUnauthorized, "missing API key", "invalid_request_error", "missing_key")
	}
	prin, err := h.cache.Get(ctx, secure.HashAPIKey(token))
	if errors.Is(err, store.ErrNotFound) {
		return deny(http.StatusUnauthorized, "invalid API key", "invalid_request_error", "invalid_key")
	}
	if err != nil {
		h.deps.Log.Warn("key lookup failed", "err", err)
		p, a, g := deny(http.StatusServiceUnavailable, "gateway database temporarily unavailable, retry shortly", "server_error", "gateway_unavailable")
		g.retryAfter = "5"
		return p, a, g
	}
	if prin.EmployeeStatus != "active" {
		return deny(http.StatusForbidden, "account disabled", "invalid_request_error", "employee_disabled")
	}
	if prin.Frozen {
		return deny(http.StatusForbidden, "this key is suspended ("+prin.FrozenReason+"); contact the administrator", "invalid_request_error", "account_frozen")
	}
	if prin.NoticeAckVersion < set.NoticeVersion {
		return deny(http.StatusForbidden, "usage notice not acknowledged; sign in to the portal first", "invalid_request_error", "notice_required")
	}
	if prin.Account == nil {
		return deny(http.StatusForbidden, "no upstream account bound to this key", "invalid_request_error", "no_account_bound")
	}
	acc := *prin.Account
	if acc.Status != "active" {
		return deny(http.StatusForbidden, "upstream account disabled", "invalid_request_error", "account_disabled")
	}
	if acc.NeedsLogin {
		return deny(http.StatusServiceUnavailable, "account needs re-login; the administrator has been notified", "server_error", "account_needs_login")
	}
	if acc.RateLimitedUntil != nil && acc.RateLimitedUntil.After(time.Now()) {
		p, a, g := deny(http.StatusTooManyRequests, "account rate limited", "rate_limit_error", "account_rate_limited")
		g.retryAfter = strconv.Itoa(int(time.Until(*acc.RateLimitedUntil).Seconds()) + 1)
		return p, a, g
	}
	return prin, acc, nil
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
		if k := gjson.GetBytes(body, "client_metadata.session_id").String(); k != "" {
			return k
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

// recordObserved counts a Codex client UA; each UA is written at most every
// observedEvery, carrying the uses counted since its last write.
func (h *Handler) recordObserved(hdr http.Header) {
	ua := hdr.Get("User-Agent")
	if ua == "" {
		return
	}
	ver := ""
	if p, ok := ParseCodexUA(ua); ok {
		ver = p.Version
	}
	h.bkMu.Lock()
	e := h.observed[ua]
	if e == nil {
		if len(h.observed) >= 1000 {
			h.bkMu.Unlock()
			return
		}
		e = &observedEntry{}
		h.observed[ua] = e
	}
	e.originator, e.version = hdr.Get("Originator"), ver
	e.pending++
	if time.Since(e.written) < observedEvery {
		h.bkMu.Unlock()
		return
	}
	c := store.ObservedClient{UserAgent: ua, Originator: e.originator, Version: e.version, Hits: e.pending}
	e.pending, e.written = 0, time.Now()
	h.bkMu.Unlock()

	if err := h.deps.Store.UpsertObservedClient(context.Background(), c); err != nil {
		h.bkMu.Lock()
		e.pending += c.Hits
		e.written = time.Time{}
		h.bkMu.Unlock()
	}
}

// quotaDue reports whether this response's quota headers should be stored:
// at most once per quotaEvery per account, but always on a rate limit.
func (h *Handler) quotaDue(accountID int64, status int) bool {
	h.bkMu.Lock()
	defer h.bkMu.Unlock()
	if status != http.StatusTooManyRequests && time.Since(h.quotaAt[accountID]) < quotaEvery {
		return false
	}
	h.quotaAt[accountID] = time.Now()
	return true
}

func (h *Handler) reactToUpstream(ctx context.Context, acc store.Account, resp *http.Response, model string, outcome capture.Outcome, set settings.Settings) {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		until := parseRateLimitReset(resp.Header)
		_ = h.deps.Store.SetRateLimitedUntil(ctx, acc.ID, &until)
		h.cache.forgetAccount(acc.ID)
		_, _ = h.deps.Store.AddSignal(ctx, acc.ID, "rate_limited", map[string]any{"until": until})
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_ = h.deps.Store.SetNeedsLogin(ctx, acc.ID, true)
		h.cache.forgetAccount(acc.ID)
		_, _ = h.deps.Store.AddSignal(ctx, acc.ID, "needs_login", map[string]any{"status": resp.StatusCode})
	}
	// Model mismatch (silent degradation) signal — ChatGPT accounts only; other
	// providers (e.g. Kimi during trials) report their own model aliases.
	if acc.IsChatGPT() && outcome.Model != "" && model != "" && stripPrefix(outcome.Model) != model {
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
	models, err := h.AccountModels(ctx, *prin.Account)
	if err != nil || len(models) == 0 {
		writeStaticModels(w, h.deps.Settings.Get(ctx).DefaultCodexModel)
		return
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{"id": m.ID, "object": "model", "created": m.Created, "owned_by": "company"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

// AccountModels lists the models CPA serves for an account, without the
// account's routing prefix.
func (h *Handler) AccountModels(ctx context.Context, acc store.Account) ([]store.ModelInfo, error) {
	prefix := acc.Prefix() + "/"
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	upReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, h.deps.CPABaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Authorization", "Bearer "+h.deps.CPAAPIKey)
	resp, err := h.deps.Upstream.Do(upReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var models []store.ModelInfo
	gjson.GetBytes(raw, "data").ForEach(func(_, v gjson.Result) bool {
		if id := v.Get("id").String(); strings.HasPrefix(id, prefix) {
			models = append(models, store.ModelInfo{
				ID: strings.TrimPrefix(id, prefix), Name: v.Get("display_name").String(), Created: v.Get("created").Int(),
			})
		}
		return true
	})
	return models, nil
}

// writeStaticModels answers /v1/models when CPA cannot list the account's
// models: just the configured default, which Codex can still use.
func writeStaticModels(w http.ResponseWriter, defaultModel string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   []map[string]any{{"id": defaultModel, "object": "model", "owned_by": "company"}},
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
