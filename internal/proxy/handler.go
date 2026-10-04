package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
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
	// PortalURL is the employee portal's public base URL, linked from refusal
	// messages; Loc is the time zone those messages give times in.
	PortalURL string
	Loc       *time.Location
	// CPA is CPA's management API, for calls CPA does not proxy itself
	// (Codex's standalone web search); nil turns web search off.
	CPA SearchBackend
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

	authFiles authFileCache

	// Picking among an employee's accounts: the account each conversation
	// used, and each account's latest used percentage.
	sticky stickyMap
	load   loadMap

	// CPA's model list by account, cached.
	catMu sync.Mutex
	cat   map[int64][]store.ModelInfo
	catAt time.Time
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
	r.Post("/v1/images/generations", h.handleImages("generations"))
	r.Post("/v1/images/edits", h.handleImages("edits"))
	r.Post("/v1/alpha/search", h.handleSearch)
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
		kind := h.classifier(set).Classify(r.Header)
		clientIP := risk.ClientIP(r, h.deps.TrustProxy)

		// --- 1. Authenticate ---
		prin, refused := h.authorize(ctx, bearer(r))
		if refused != nil && prin.EmployeeID == 0 {
			h.logAnonymous(r, kind, refused)
			refused.write(w, kind)
			return
		}

		// --- 2. Read body; from here on every outcome is logged ---
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			oaError(w, http.StatusBadRequest, "请求内容读取失败，请重试。", "invalid_request_error", "bad_body")
			return
		}
		model := stripPrefix(gjson.GetBytes(body, "model").String())
		stream := gjson.GetBytes(body, "stream").Bool()
		reasoning := gjson.GetBytes(body, "reasoning.effort").String()
		if reasoning == "" {
			reasoning = gjson.GetBytes(body, "reasoning_effort").String()
		}
		sessionSeed := h.sessionSeed(kind, r.Header, body, prin.EmployeeID)
		rec := recorder.Record{
			RequestID: uuid.New(), StartedAt: start, EmployeeID: prin.EmployeeID,
			APIKeyID: &prin.KeyID, SessionKey: sessionSeed,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"), Endpoint: endpoint,
			Stream: stream, ModelRequested: model, ReasoningEffort: reasoning,
			ReqBody: body, ClientIP: clientIP,
		}
		obs := risk.Observation{
			EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: clientIP, At: start,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"),
		}
		// --- 2a. Pick the account: allowed for this client and model, usable now ---
		want := need{model: model, kind: kind, session: sessionSeed, exclude: map[int64]bool{}}
		acc := store.Account{}
		if refused == nil {
			acc, refused = h.pickAccount(prin, want, set, nil)
		}
		if acc.ID != 0 {
			rec.AccountID = &acc.ID
		}
		if refused != nil {
			h.observeRefusal(obs, refused)
			h.refuse(w, refused, kind, &rec)
			return
		}
		if kind == KindCodex {
			go h.recordObserved(r.Header)
		}

		// --- 2b. Gates: office network, client type, content audit, device ---
		devInfo := risk.ExtractDevice(r.Header, body)
		deny := func(d risk.Decision) {
			obs.Denied = d.Code
			h.observe(obs)
			h.refuse(w, fromDecision(d), kind, &rec)
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
		if h.deps.Risk != nil {
			dev, created, err := h.deps.Risk.Device(ctx, prin.EmployeeID, devInfo, kind, clientIP)
			if err != nil {
				// Fail open: detection must not take the service down with the DB.
				h.deps.Log.Warn("risk: device lookup failed", "employee", prin.EmployeeID, "err", err)
			} else {
				rec.DeviceID = &dev.ID
				obs.Device, obs.NewDevice = dev, created
				if d := risk.DeviceGate(set, dev, created, time.Now(), h.deps.Risk.PortalDevicesURL()); !d.Allow {
					deny(d)
					return
				}
			}
		}
		if obs.ContentBlocked {
			deny(contentBlocked(obs.ContentRule))
			return
		}
		h.observe(obs)

		// --- 3..5. Send upstream; an account whose quota runs out hands the
		// request to the employee's next usable account ---
		for {
			resp, release, f := h.forward(ctx, w, r, acc, body, model, endpoint, kind, sessionSeed, stream, set, &rec)
			if f != nil {
				if f.code == "account_rate_limited" {
					want.exclude[acc.ID] = true
					if next, nf := h.pickAccount(prin, want, set, nil); nf == nil {
						h.deps.Log.Info("account used up, switching", "employee", prin.EmployeeID, "from", acc.ID, "to", next.ID)
						acc = next
						rec.AccountID = &acc.ID
						h.sticky.put(prin.EmployeeID, sessionSeed, acc.ID)
						continue
					}
				}
				h.refuse(w, f, kind, &rec)
				return
			}
			if resp == nil {
				return // already answered (client gone, upstream unreachable)
			}
			h.relay(ctx, w, resp, acc, endpoint, model, stream, start, set, &rec)
			release()
			return
		}
	}
}

// forward sends the request upstream on acc. It returns the response to relay
// (with the release of its concurrency slot), or a refusal; a nil response
// with no refusal means the outcome was already written and logged.
func (h *Handler) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, acc store.Account, body []byte,
	model, endpoint, kind, sessionSeed string, stream bool, set settings.Settings, rec *recorder.Record) (*http.Response, func(), *refusal) {
	// --- 3. Concurrency gate ---
	capN := set.DefaultMaxConcurrency
	if acc.MaxConcurrency != nil && *acc.MaxConcurrency > 0 {
		capN = *acc.MaxConcurrency
	}
	release, waited, err := h.deps.Limiter.Acquire(ctx, acc.ID, capN, time.Duration(set.QueueTimeoutSeconds)*time.Second)
	if err != nil {
		if errors.Is(err, ErrQueueTimeout) {
			return nil, nil, gatewayBusy()
		}
		// context cancelled by client
		w.WriteHeader(499)
		return nil, nil, nil
	}
	rec.QueuedMS += int(waited.Milliseconds())

	// --- 4. Build upstream request ---
	upstreamModel := acc.Prefix() + "/" + model
	outBody, _ := sjson.SetBytes(body, "model", upstreamModel)

	hdr := BuildUpstreamHeaders(HeaderInput{
		Kind:      kind,
		Client:    r.Header,
		Profile:   set.CodexProfile,
		Device:    acc.DeviceProfile,
		SessionID: SessionUUID(sessionSeed),
		CPAAPIKey: h.deps.CPAAPIKey,
	})

	upURL := h.deps.CPABaseURL + "/v1/" + endpointPath(endpoint, r.URL.Path)
	timeout := 100 * time.Second
	if stream {
		timeout = 600 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	done := func() { cancel(); release() }

	upReq, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, upURL, strings.NewReader(string(outBody)))
	upReq.Header = hdr

	resp, err := h.deps.Upstream.Do(upReq)
	if err != nil {
		done()
		rec.StatusCode = http.StatusBadGateway
		rec.ErrorType = "upstream_unreachable"
		rec.ErrorBody = err.Error()
		rec.DurationMS = int(time.Since(rec.StartedAt).Milliseconds())
		h.deps.Recorder.Submit(*rec)
		oaError(w, http.StatusBadGateway, "上游服务暂时连不上，请稍后重试。", "server_error", "upstream_unreachable")
		return nil, nil, nil
	}
	h.noteQuota(acc.ID, resp.StatusCode, resp.Header)
	// A used-up quota: the caller tries another account, or answers in Codex's
	// usage-limit shape, without the upstream plan's upgrade advice.
	if resp.StatusCode == http.StatusTooManyRequests {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if until, ok := quotaReset(raw, resp.Header, time.Now()); ok {
			done()
			rec.ErrorBody = string(raw[:min(len(raw), 2000)])
			h.markRateLimited(context.WithoutCancel(ctx), acc.ID, until, nil)
			return nil, nil, h.usageLimited(until)
		}
		resp.Body = io.NopCloser(bytes.NewReader(raw))
	}
	return resp, done, nil
}

// relay copies the upstream response to the client, logs it and reacts to it.
func (h *Handler) relay(ctx context.Context, w http.ResponseWriter, resp *http.Response, acc store.Account,
	endpoint, model string, stream bool, start time.Time, set settings.Settings, rec *recorder.Record) {
	defer resp.Body.Close()
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
	h.deps.Recorder.Submit(*rec)

	// --- 6. React to upstream status / degradation ---
	h.reactToUpstream(context.WithoutCancel(ctx), acc, resp, model, outcome, set)
}

// noteQuota records the quota an upstream response reports: in memory for
// picking accounts, and as a snapshot at most once per quotaEvery.
func (h *Handler) noteQuota(accountID int64, status int, hdr http.Header) {
	if !health.HasQuotaHeaders(hdr) {
		return
	}
	if pct, ok := health.UsedPctFromHeaders(hdr); ok {
		h.load.set(accountID, pct)
	}
	if h.quotaDue(accountID, status) {
		go func(hdr http.Header) {
			_ = health.RecordQuotaFromHeaders(context.Background(), h.deps.Store, accountID, hdr, time.Now())
		}(hdr.Clone())
	}
}

func gatewayBusy() *refusal {
	return &refusal{status: http.StatusTooManyRequests, typ: "rate_limit_error", code: "gateway_busy",
		msg: "同时进行的请求太多，稍后会自动重试。", retryAfter: "1"}
}

// observeRefusal tells the risk engine about an employee who has a key but
// no account yet (the administrators hear about it), and about a client the
// employee's accounts are not open to (the employee hears about it).
func (h *Handler) observeRefusal(o risk.Observation, f *refusal) {
	if f.code == "no_account_bound" || f.code == "client_not_allowed" {
		o.Denied = f.code
		h.observe(o)
	}
}

func (h *Handler) observe(o risk.Observation) {
	if h.deps.Risk != nil {
		h.deps.Risk.Observe(o)
	}
}

// authorize resolves a bearer token to its principal and applies the employee
// checks shared by every endpoint; which account serves the request is picked
// afterwards (pickAccount). A refusal after the key is found still returns the
// principal, so the refusal can be logged for the employee.
func (h *Handler) authorize(ctx context.Context, token string) (store.KeyPrincipal, *refusal) {
	no := func(status int, code, msg string) *refusal {
		return &refusal{status: status, typ: "invalid_request_error", code: code, msg: msg}
	}
	if token == "" {
		return store.KeyPrincipal{}, no(http.StatusUnauthorized, "missing_key",
			"没有带上 Key。请到 "+h.portal("/key")+" 重新配置（生成 Key 时会给出安装命令）。")
	}
	hash := secure.HashAPIKey(token)
	prin, err := h.cache.Get(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		// A reset key is the usual cause: say so, and log it for its owner.
		if keyID, empID, err := h.deps.Store.RevokedKeyOwner(ctx, hash); err == nil {
			return store.KeyPrincipal{KeyID: keyID, EmployeeID: empID}, no(http.StatusUnauthorized, "key_revoked",
				"这个 Key 已经重置过，旧 Key 不能再用了。请到 "+h.portal("/key")+" 用新 Key 重新配置（弹窗里有一条命令配好的方式）。")
		}
		return store.KeyPrincipal{}, no(http.StatusUnauthorized, "invalid_key",
			"Key 无效。请到 "+h.portal("/key")+" 生成新 Key，按弹窗里的安装命令配置。")
	}
	if err != nil {
		h.deps.Log.Warn("key lookup failed", "err", err)
		return store.KeyPrincipal{}, &refusal{status: http.StatusServiceUnavailable,
			typ: "server_error", code: "gateway_unavailable", msg: "网关暂时连不上数据库，稍后会自动重试。", retryAfter: "5"}
	}
	switch {
	case prin.EmployeeStatus != "active":
		return prin, no(http.StatusForbidden, "employee_disabled", "你的账号已停用，如有疑问请联系管理员。")
	case prin.Frozen:
		why := ""
		if prin.FrozenReason != "" {
			why = "（" + prin.FrozenReason + "）"
		}
		return prin, no(http.StatusForbidden, "account_frozen", "你的 Key 已被暂停"+why+"，请联系管理员。")
	}
	return prin, nil
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
		h.markRateLimited(ctx, acc.ID, parseRateLimitReset(resp.Header), nil)
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

// markRateLimited stops routing to an account until its limit resets.
func (h *Handler) markRateLimited(ctx context.Context, accountID int64, until time.Time, extra map[string]any) {
	_ = h.deps.Store.SetRateLimitedUntil(ctx, accountID, &until)
	h.cache.forgetAccount(accountID)
	detail := map[string]any{"until": until}
	for k, v := range extra {
		detail[k] = v
	}
	_, _ = h.deps.Store.AddSignal(ctx, accountID, "rate_limited", detail)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	set := h.deps.Settings.Get(ctx)
	token := bearer(r)
	kind := h.classifier(set).Classify(r.Header)
	if token == "" {
		h.logAnonymous(r, kind, &refusal{code: "missing_key"})
		oaError(w, http.StatusUnauthorized, "没有带上 Key。", "invalid_request_error", "missing_key")
		return
	}
	prin, err := h.cache.Get(ctx, secure.HashAPIKey(token))
	if err != nil && errors.Is(err, store.ErrNotFound) {
		h.logAnonymous(r, kind, &refusal{code: "invalid_key"})
	}
	if err != nil || len(prin.Accounts) == 0 {
		oaError(w, http.StatusForbidden, "Key 无效，或还没有分配 AI 账号。", "invalid_request_error", "no_account_bound")
		return
	}
	if prin.Frozen || prin.EmployeeStatus != "active" {
		oaError(w, http.StatusForbidden, "你的 Key 已被暂停，请联系管理员。", "invalid_request_error", "account_frozen")
		return
	}
	// The models of the accounts this client may use, as their policies allow.
	var accs []store.Account
	for _, a := range prin.Accounts {
		if a.Status == "active" && (kind == KindCodex || set.AllowNonCodex || a.AllowThirdParty) {
			accs = append(accs, a)
		}
	}
	models, err := h.ModelsFor(ctx, accs)
	if err != nil || len(models) == 0 {
		writeStaticModels(w, set.DefaultCodexModel)
		return
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{"id": m.ID, "object": "model", "created": m.Created, "owned_by": "company"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

// AccountModels lists every model CPA serves for an account, without the
// account's routing prefix and regardless of the account's model policy.
func (h *Handler) AccountModels(ctx context.Context, acc store.Account) ([]store.ModelInfo, error) {
	cat, err := h.catalog(ctx)
	if err != nil {
		return nil, err
	}
	return cat[acc.ID], nil
}

// ModelsFor lists the models an employee may use across the given accounts:
// each account's models as its policy allows, without duplicates, newest first.
func (h *Handler) ModelsFor(ctx context.Context, accounts []store.Account) ([]store.ModelInfo, error) {
	cat, err := h.catalog(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []store.ModelInfo
	for _, a := range accounts {
		for _, m := range cat[a.ID] {
			if !seen[m.ID] && a.AllowsModel(m.ID) {
				seen[m.ID] = true
				out = append(out, m)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

// catalog is CPA's model list by account (cached for a minute): CPA lists
// every account's models under its "acct-N/" prefix.
func (h *Handler) catalog(ctx context.Context) (map[int64][]store.ModelInfo, error) {
	h.catMu.Lock()
	if h.cat != nil && time.Since(h.catAt) < time.Minute {
		defer h.catMu.Unlock()
		return h.cat, nil
	}
	h.catMu.Unlock()

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
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("cpa /v1/models: " + resp.Status)
	}
	cat := map[int64][]store.ModelInfo{}
	gjson.GetBytes(raw, "data").ForEach(func(_, v gjson.Result) bool {
		id := v.Get("id").String()
		prefix, model, ok := strings.Cut(id, "/")
		if !ok || !strings.HasPrefix(prefix, "acct-") {
			return true
		}
		acc, err := strconv.ParseInt(strings.TrimPrefix(prefix, "acct-"), 10, 64)
		if err != nil {
			return true
		}
		cat[acc] = append(cat[acc], store.ModelInfo{ID: model, Name: v.Get("display_name").String(), Created: v.Get("created").Int()})
		return true
	})
	h.catMu.Lock()
	h.cat, h.catAt = cat, time.Now()
	h.catMu.Unlock()
	return cat, nil
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
