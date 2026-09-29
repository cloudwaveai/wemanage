package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/cpa"
	"aiseat/internal/recorder"
	"aiseat/internal/risk"
	"aiseat/internal/settings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// Codex's standalone web search (the web.run tool, which models on the
// Responses Lite protocol such as luna use instead of the hosted web_search
// tool) posts to {base_url}/alpha/search. CPA has no such route, so the
// gateway makes the call itself through CPA's management api-call: CPA puts
// in the account's token and goes out through its proxy, as the quota probe
// does for usage.
const (
	codexSearchURL     = "https://chatgpt.com/backend-api/codex/alpha/search"
	searchTimeout      = 90 * time.Second
	maxSearchBodyBytes = 8 << 20
	searchEndpointName = "search"
	authFilesMaxAge    = 5 * time.Minute
)

// SearchBackend is what web search needs from CPA's management API.
type SearchBackend interface {
	ListAuthFiles(ctx context.Context) ([]cpa.AuthFile, error)
	APICall(ctx context.Context, call cpa.APICallRequest) (cpa.APICallResponse, error)
}

// authFileCache keeps CPA's credential list for a few minutes, so a search
// does not list every credential first.
type authFileCache struct {
	mu    sync.Mutex
	files map[string]cpa.AuthFile
	at    time.Time
}

func (h *Handler) authFile(ctx context.Context, name string) (cpa.AuthFile, error) {
	c := &h.authFiles
	c.mu.Lock()
	f, ok := c.files[name]
	fresh := time.Since(c.at) < authFilesMaxAge
	c.mu.Unlock()
	if ok && fresh {
		return f, nil
	}
	list, err := h.deps.CPA.ListAuthFiles(ctx)
	if err != nil {
		return cpa.AuthFile{}, err
	}
	files := make(map[string]cpa.AuthFile, len(list))
	for _, f := range list {
		files[f.Name] = f
	}
	c.mu.Lock()
	c.files, c.at = files, time.Now()
	c.mu.Unlock()
	if f, ok := files[name]; ok && f.AuthIndex != "" {
		return f, nil
	}
	return cpa.AuthFile{}, fmt.Errorf("auth file %s not found in CPA", name)
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()
	set := h.deps.Settings.Get(ctx)

	kind := h.classifier(set).Classify(r.Header)
	prin, acc, refused := h.authorize(ctx, bearer(r), set)
	if refused != nil && prin.EmployeeID == 0 {
		h.logAnonymous(r, kind, refused)
		refused.write(w, kind)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSearchBodyBytes))
	if err != nil || !gjson.ValidBytes(body) {
		oaError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error", "bad_body")
		return
	}

	queries := searchQueries(body)
	logBody := searchLogBody(queries)
	clientIP := risk.ClientIP(r, h.deps.TrustProxy)
	rec := recorder.Record{
		RequestID: uuid.New(), StartedAt: start, EmployeeID: prin.EmployeeID,
		AccountID: accountID(prin), APIKeyID: &prin.KeyID,
		SessionKey: h.sessionSeed(kind, r.Header, body, prin.EmployeeID),
		ClientKind: kind, UserAgent: r.Header.Get("User-Agent"), Endpoint: searchEndpointName,
		ModelRequested: gjson.GetBytes(body, "model").String(), ReqBody: logBody, ClientIP: clientIP,
	}
	obs := risk.Observation{
		EmployeeID: prin.EmployeeID, OpenID: prin.EmployeeOpenID, IP: clientIP, At: start,
		ClientKind: kind, UserAgent: r.Header.Get("User-Agent"),
	}
	if refused != nil {
		h.observeRefusal(obs, refused)
		h.refuse(w, refused, kind, &rec)
		return
	}
	if !acc.IsChatGPT() || h.deps.CPA == nil {
		h.refuse(w, &refusal{status: http.StatusNotFound, typ: "invalid_request_error", code: "search_unsupported",
			msg: "联网搜索需要 ChatGPT 账号，你现在的 AI 账号不支持。"}, kind, &rec)
		return
	}

	deny := func(d risk.Decision) {
		obs.Denied = d.Code
		h.observe(obs)
		h.refuse(w, fromDecision(d), kind, &rec)
	}
	if d := risk.NetworkGate(set, prin.RemoteAllowed, clientIP, &h.office); !d.Allow {
		deny(d)
		return
	}
	// Like image calls, searches carry no installation id: only the client
	// type is checked, and no device is registered for them.
	if d := risk.ClientGate(set, kind, risk.DeviceInfo{InstallID: "n/a"}); !d.Allow {
		deny(d)
		return
	}
	if hit, flagged := h.auditor.Check(set, searchEndpointName, logBody); flagged {
		obs.ContentRule, obs.ContentSnippet = hit.Rule, hit.Snippet
		obs.ContentBlocked = set.ContentAuditMode == settings.AuditBlock
	}
	if obs.ContentBlocked {
		deny(contentBlocked(obs.ContentRule))
		return
	}
	h.observe(obs)

	capN := set.DefaultMaxConcurrency
	if acc.MaxConcurrency != nil && *acc.MaxConcurrency > 0 {
		capN = *acc.MaxConcurrency
	}
	release, waited, err := h.deps.Limiter.Acquire(ctx, acc.ID, capN, time.Duration(set.QueueTimeoutSeconds)*time.Second)
	if err != nil {
		if errors.Is(err, ErrQueueTimeout) {
			h.refuse(w, gatewayBusy(), kind, &rec)
			return
		}
		w.WriteHeader(499)
		return
	}
	defer release()
	rec.QueuedMS = int(waited.Milliseconds())

	fail := func(status int, code, msg string, cause error) {
		rec.StatusCode, rec.ErrorType = status, code
		if cause != nil {
			rec.ErrorBody = cause.Error()
		}
		rec.DurationMS = int(time.Since(start).Milliseconds())
		h.deps.Recorder.Submit(rec)
		oaError(w, status, msg, "server_error", code)
	}
	f, err := h.authFile(ctx, acc.CPAAuthName)
	if err != nil {
		fail(http.StatusBadGateway, "upstream_unreachable", "上游服务暂时连不上，请稍后重试。", err)
		return
	}
	call := cpa.APICallRequest{
		AuthIndex: f.AuthIndex, Method: http.MethodPost, URL: codexSearchURL, Data: string(body),
		Header: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"Accept":        "application/json",
		},
	}
	if id := f.IDToken.ChatGPTAccountID; id != "" {
		call.Header["ChatGPT-Account-Id"] = id
	}
	// The client's identity headers, as Codex would send them to ChatGPT.
	for _, k := range []string{"User-Agent", "Originator", "Version", "X-Codex-Turn-Metadata"} {
		if v := r.Header.Get(k); v != "" && (kind == KindCodex || k == "X-Codex-Turn-Metadata") {
			call.Header[k] = v
		}
	}
	if kind != KindCodex {
		call.Header["User-Agent"] = RenderUA(set.CodexProfile, acc.DeviceProfile)
		call.Header["Originator"] = set.CodexProfile.Originator
	}

	callCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	resp, err := h.deps.CPA.APICall(callCtx, call)
	if err != nil {
		fail(http.StatusBadGateway, "upstream_unreachable", "上游服务暂时连不上，请稍后重试。", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.WriteString(w, resp.Body)

	rec.StatusCode = resp.StatusCode
	rec.DurationMS = int(time.Since(start).Milliseconds())
	rec.Outcome = searchOutcome(rec.ModelRequested, resp.StatusCode, []byte(resp.Body))
	if resp.StatusCode >= 400 {
		rec.ErrorType = firstNonEmpty(gjson.Get(resp.Body, "error.code").String(), fmt.Sprintf("upstream_%d", resp.StatusCode))
		rec.ErrorBody = resp.Body[:min(len(resp.Body), 2000)]
	}
	h.deps.Recorder.Submit(rec)
}

// searchQueries lists what a search call asks for: its queries, image
// queries and pages to open.
func searchQueries(body []byte) []string {
	var out []string
	cmds := gjson.GetBytes(body, "commands")
	cmds.Get("search_query.#.q").ForEach(func(_, v gjson.Result) bool {
		out = append(out, v.String())
		return true
	})
	cmds.Get("image_query.#.q").ForEach(func(_, v gjson.Result) bool {
		out = append(out, "图片："+v.String())
		return true
	})
	cmds.Get("open.#.ref_id").ForEach(func(_, v gjson.Result) bool {
		out = append(out, "打开："+v.String())
		return true
	})
	return out
}

// searchLogBody is what the log keeps of a search request: its queries as a
// user turn, in the Responses shape the recorder and auditor understand. The
// conversation excerpt Codex sends along is already in the chat turns' logs.
func searchLogBody(queries []string) []byte {
	b, _ := json.Marshal(map[string]any{
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "[联网搜索] " + strings.Join(queries, "；")}},
		}},
	})
	return b
}

// searchOutcome summarizes a search response for the log: how many results
// and where they came from.
func searchOutcome(model string, status int, body []byte) capture.Outcome {
	out := capture.Outcome{Model: model, Completed: status < 400}
	if status >= 400 {
		return out
	}
	results := gjson.GetBytes(body, "results").Array()
	lines := []string{fmt.Sprintf("[搜索到 %d 条结果]", len(results))}
	for i, res := range results {
		if i == 10 {
			break
		}
		if u := res.Get("url").String(); u != "" {
			lines = append(lines, u)
		}
	}
	item, _ := json.Marshal(map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": strings.Join(lines, "\n")}},
	})
	out.OutputItems = []json.RawMessage{item}
	return out
}
