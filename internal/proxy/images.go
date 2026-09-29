package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/recorder"
	"aiseat/internal/risk"
	"aiseat/internal/settings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Codex generates and edits images through the provider's Images API
// (POST {base_url}/images/generations and /images/edits, JSON bodies). CPA
// serves those for ChatGPT accounts from chatgpt.com/backend-api/codex/images,
// the endpoint the official client uses when signed in with ChatGPT.
const (
	imagesTimeout      = 5 * time.Minute
	defaultImageModel  = "gpt-image-1.5"
	maxImageBodyBytes  = 64 << 20
	imagesEndpointName = "images"
)

// handleImages proxies one Images API call ("generations" or "edits") for a
// ChatGPT account: the usual key, network, client, content and concurrency
// gates, the account's model prefix, and a log entry holding the prompt and a
// summary of the result (never the image data itself).
func (h *Handler) handleImages(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		set := h.deps.Settings.Get(ctx)

		prin, acc, ge := h.authorize(ctx, bearer(r), set)
		if ge != nil {
			ge.write(w)
			return
		}
		if !acc.IsChatGPT() {
			oaError(w, http.StatusNotFound, "image generation needs a ChatGPT account", "invalid_request_error", "images_unsupported")
			return
		}
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			oaError(w, http.StatusUnsupportedMediaType, "only JSON image requests are supported", "invalid_request_error", "unsupported_media_type")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxImageBodyBytes))
		if err != nil || !gjson.ValidBytes(body) {
			oaError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error", "bad_body")
			return
		}

		kind := h.classifier(set).Classify(r.Header)
		clientIP := risk.ClientIP(r, h.deps.TrustProxy)
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
		// Image calls carry no Codex installation id (the chat turns that lead
		// to them do), so only the client type is checked and no device is
		// registered for them.
		if d := risk.ClientGate(set, kind, risk.DeviceInfo{InstallID: "n/a"}); !d.Allow {
			deny(d)
			return
		}

		model := stripPrefix(gjson.GetBytes(body, "model").String())
		if model == "" {
			model = defaultImageModel
		}
		logBody := imageLogBody(op, model, gjson.GetBytes(body, "prompt").String())
		if hit, flagged := h.auditor.Check(set, imagesEndpointName, logBody); flagged {
			obs.ContentRule, obs.ContentSnippet = hit.Rule, hit.Snippet
			obs.ContentBlocked = set.ContentAuditMode == settings.AuditBlock
		}
		if obs.ContentBlocked {
			deny(risk.Decision{Status: http.StatusForbidden, Code: "content_blocked",
				Message: "request blocked by the company's AI usage policy (" + obs.ContentRule + "); contact the administrator if this is a mistake"})
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
				w.Header().Set("Retry-After", "1")
				oaError(w, http.StatusTooManyRequests, "gateway concurrency limit reached, retry shortly", "rate_limit_error", "gateway_busy")
				return
			}
			w.WriteHeader(499)
			return
		}
		defer release()

		sessionSeed := h.sessionSeed(kind, r.Header, body, prin.EmployeeID)
		outBody, _ := sjson.SetBytes(body, "model", acc.Prefix()+"/"+model)
		hdr := BuildUpstreamHeaders(HeaderInput{
			Kind: kind, Client: r.Header, Profile: set.CodexProfile, Device: acc.DeviceProfile,
			SessionID: SessionUUID(sessionSeed), CPAAPIKey: h.deps.CPAAPIKey,
		})
		if r.Header.Get("Accept") == "" {
			hdr.Set("Accept", "application/json")
		}

		reqCtx, cancel := context.WithTimeout(ctx, imagesTimeout)
		defer cancel()
		upReq, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, h.deps.CPABaseURL+"/v1/images/"+op, strings.NewReader(string(outBody)))
		upReq.Header = hdr

		rec := recorder.Record{
			RequestID: uuid.New(), StartedAt: start, EmployeeID: prin.EmployeeID,
			AccountID: &acc.ID, APIKeyID: &prin.KeyID, SessionKey: sessionSeed,
			ClientKind: kind, UserAgent: r.Header.Get("User-Agent"), Endpoint: imagesEndpointName,
			ModelRequested: model, QueuedMS: int(waited.Milliseconds()), ReqBody: logBody, ClientIP: clientIP,
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
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxImageBodyBytes))
		copyResponseHeaders(w, resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)

		rec.StatusCode = resp.StatusCode
		rec.DurationMS = int(time.Since(start).Milliseconds())
		rec.Outcome = imageOutcome(op, model, resp.StatusCode, respBody)
		if resp.StatusCode >= 400 {
			rec.ErrorType = firstNonEmpty(gjson.GetBytes(respBody, "error.code").String(), fmt.Sprintf("upstream_%d", resp.StatusCode))
			rec.ErrorBody = string(respBody[:min(len(respBody), 2000)])
		}
		h.deps.Recorder.Submit(rec)

		// An expired login breaks everything; a 429 here is the separate image
		// quota, so it must not mark the account rate limited for chat.
		if resp.StatusCode == http.StatusUnauthorized {
			bg := context.WithoutCancel(ctx)
			_ = h.deps.Store.SetNeedsLogin(bg, acc.ID, true)
			h.cache.forgetAccount(acc.ID)
			_, _ = h.deps.Store.AddSignal(bg, acc.ID, "needs_login", map[string]any{"status": resp.StatusCode, "endpoint": "images"})
		}
	}
}

// imageLogBody is what the log keeps of an image request: the prompt as a
// user turn, in the Responses shape the recorder and auditor understand.
// Input images of an edit are not kept.
func imageLogBody(op, model, prompt string) []byte {
	text := prompt
	if op == "edits" {
		text = "[编辑图片] " + prompt
	}
	b, _ := json.Marshal(map[string]any{
		"model": model,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		}},
	})
	return b
}

// imageOutcome summarizes an Images API response for the log: how many
// images, their size/quality, the revised prompt and token usage.
func imageOutcome(op, model string, status int, body []byte) capture.Outcome {
	out := capture.Outcome{Model: model, Completed: status < 400}
	if status >= 400 {
		return out
	}
	n := len(gjson.GetBytes(body, "data").Array())
	verb := "生成了"
	if op == "edits" {
		verb = "编辑得到"
	}
	parts := []string{fmt.Sprintf("[%s %d 张图片]", verb, n)}
	for _, k := range []string{"size", "quality", "output_format"} {
		if v := gjson.GetBytes(body, k).String(); v != "" {
			parts = append(parts, v)
		}
	}
	text := strings.Join(parts, " · ")
	if rp := gjson.GetBytes(body, "data.0.revised_prompt").String(); rp != "" {
		text += "\n修订后的提示词：" + rp
	}
	item, _ := json.Marshal(map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	})
	out.OutputItems = []json.RawMessage{item}
	u := gjson.GetBytes(body, "usage")
	out.Usage = capture.Usage{Input: u.Get("input_tokens").Int(), Output: u.Get("output_tokens").Int()}
	return out
}
