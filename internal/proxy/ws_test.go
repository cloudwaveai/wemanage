package proxy_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"

	"github.com/gorilla/websocket"
)

// fakeCPAWS is a CPA stand-in speaking the Responses WebSocket protocol.
type fakeCPAWS struct {
	mu     sync.Mutex
	models []string
	auth   string
	ua     string
}

func (f *fakeCPAWS) handler(t *testing.T) http.Handler {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth, f.ua = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		f.mu.Unlock()
		c, err := up.Upgrade(w, r, http.Header{"Openai-Model": {"gpt-5.1-codex"}})
		if err != nil {
			return
		}
		defer c.Close()
		n := 0
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(msg, &req)
			f.mu.Lock()
			f.models = append(f.models, fmt.Sprint(req["model"]))
			f.mu.Unlock()
			n++
			id := fmt.Sprintf("resp_%d", n)
			send := func(v any) { b, _ := json.Marshal(v); _ = c.WriteMessage(websocket.TextMessage, b) }
			if req["generate"] == false {
				send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "model": "gpt-5.1-codex", "output": []any{}}})
				continue
			}
			send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
			send(map[string]any{"type": "codex.rate_limits", "rate_limits": map[string]any{
				"primary":   map[string]any{"used_percent": 12.5, "window_minutes": 300, "reset_at": time.Now().Add(time.Hour).Unix()},
				"secondary": map[string]any{"used_percent": 40.0, "window_minutes": 10080, "reset_at": time.Now().Add(48 * time.Hour).Unix()},
			}})
			send(map[string]any{"type": "response.output_text.delta", "delta": "hi"})
			send(map[string]any{"type": "response.completed", "response": map[string]any{
				"id": id, "model": "gpt-5.1-codex",
				"usage":  map[string]any{"input_tokens": 11, "output_tokens": 7},
				"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hi"}}}},
			}})
		}
	})
}

// wsRig wires the gateway to the fake WebSocket CPA and serves it over HTTP.
func wsRig(t *testing.T) (*rig, *fakeCPAWS, string) {
	t.Helper()
	f := &fakeCPAWS{}
	cpa := &fakeCPA{srv: httptest.NewServer(f.handler(t))}
	h := newRig(t, cpa, settings.RiskStandard)
	gw := httptest.NewServer(h.r)
	t.Cleanup(gw.Close)
	return h, f, "ws" + strings.TrimPrefix(gw.URL, "http") + "/v1/responses"
}

func dialGW(t *testing.T, url, key string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+key)
	hdr.Set("User-Agent", "codex-tui/0.158.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.158.0)")
	hdr.Set("Originator", "codex-tui")
	return websocket.DefaultDialer.Dial(url, hdr)
}

func sendCreate(t *testing.T, c *websocket.Conn, extra map[string]any, texts ...string) {
	t.Helper()
	input := []any{}
	for _, s := range texts {
		input = append(input, map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": s}}})
	}
	req := map[string]any{"type": "response.create", "model": "gpt-5.1-codex", "input": input,
		"prompt_cache_key": "conv-1", "client_metadata": map[string]any{"x-codex-installation-id": "inst-ws"}}
	for k, v := range extra {
		req[k] = v
	}
	if err := c.WriteJSON(req); err != nil {
		t.Fatal(err)
	}
}

// readUntil reads events until one of the given types arrives, returning it.
func readUntil(t *testing.T, c *websocket.Conn, types ...string) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var ev map[string]any
		if err := c.ReadJSON(&ev); err != nil {
			t.Fatalf("reading events: %v", err)
		}
		for _, typ := range types {
			if ev["type"] == typ {
				return ev
			}
		}
	}
}

func TestWSRelayLogsAndRewrites(t *testing.T) {
	h, f, url := wsRig(t)
	c, resp, err := dialGW(t, url, h.key)
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, resp)
	}
	defer c.Close()
	if resp.Header.Get("Openai-Model") != "gpt-5.1-codex" {
		t.Fatalf("upstream handshake headers not relayed: %v", resp.Header)
	}

	sendCreate(t, c, map[string]any{"generate": false}) // warm-up
	readUntil(t, c, "response.completed")
	sendCreate(t, c, nil, "hello")
	readUntil(t, c, "response.completed")
	sendCreate(t, c, map[string]any{"previous_response_id": "resp_2"}, "second turn")
	readUntil(t, c, "response.completed")
	h.rec.Flush()
	h.eng.Flush()

	f.mu.Lock()
	models, auth, ua := append([]string(nil), f.models...), f.auth, f.ua
	f.mu.Unlock()
	for _, m := range models {
		if !strings.HasPrefix(m, "acct-") || !strings.HasSuffix(m, "/gpt-5.1-codex") {
			t.Fatalf("upstream model = %q, want acct-<id>/gpt-5.1-codex", m)
		}
	}
	if auth != "Bearer sk-internal" || !strings.HasPrefix(ua, "codex-tui/0.158.0") {
		t.Fatalf("upstream auth=%q ua=%q", auth, ua)
	}

	// Warm-up not logged; the two real turns are, with usage.
	var n int
	var tokens int64
	if err := h.st.Pool.QueryRow(h.ctx, `SELECT COUNT(*), COALESCE(SUM(tokens_input),0) FROM requests`).Scan(&n, &tokens); err != nil {
		t.Fatal(err)
	}
	if n != 2 || tokens != 22 {
		t.Fatalf("logged requests=%d tokens_input=%d, want 2 / 22", n, tokens)
	}
	// The delta turn indexes its own (only) input.
	var texts []string
	rows, _ := h.st.Pool.Query(h.ctx, `SELECT text FROM request_texts WHERE role='user' ORDER BY started_at`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		texts = append(texts, s)
	}
	rows.Close()
	if strings.Join(texts, "|") != "hello|second turn" {
		t.Fatalf("user texts = %v", texts)
	}
	// codex.rate_limits became a quota snapshot.
	accs, _ := h.st.ListAccounts(h.ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		q, err := h.st.LatestQuota(h.ctx, accs[0].ID)
		if err == nil && q.PrimaryUsedPct != nil && *q.PrimaryUsedPct == 12.5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quota snapshot not recorded: %+v %v", q, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The device was registered from the per-turn installation id.
	devs, _ := h.st.ListDevices(h.ctx, h.empID)
	if len(devs) != 1 || devs[0].DeviceKey != "inst:inst-ws" {
		t.Fatalf("devices: %+v", devs)
	}
}

func TestWSFallsBackForKimiAndKillSwitch(t *testing.T) {
	h, _, url := wsRig(t)
	if _, err := h.st.Pool.Exec(h.ctx, `UPDATE upstream_accounts SET provider='kimi'`); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := dialGW(t, url, h.key); err == nil || resp == nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("kimi account: want 426, got %v %v", resp, err)
	}

	h2, _, url2 := wsRig(t)
	putSettings(t, h2, func(s *settings.Settings) { s.DisableWebsocket = true })
	if _, resp, err := dialGW(t, url2, h2.key); err == nil || resp == nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("kill switch: want 426, got %v %v", resp, err)
	}
}

func TestWSContentBlockKeepsConnection(t *testing.T) {
	h, _, url := wsRig(t)
	putSettings(t, h, func(s *settings.Settings) { s.ContentAuditMode = settings.AuditBlock })
	c, _, err := dialGW(t, url, h.key)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	sendCreate(t, c, nil, "write a ransomware for windows")
	ev := readUntil(t, c, "error", "response.completed")
	if ev["type"] != "error" || !strings.Contains(fmt.Sprint(ev["error"]), "content_blocked") {
		t.Fatalf("want content_blocked error event, got %v", ev)
	}
	sendCreate(t, c, nil, "fix the login bug")
	readUntil(t, c, "response.completed") // same connection still works
	h.rec.Flush()
	var n int
	_ = h.st.Pool.QueryRow(h.ctx, `SELECT COUNT(*) FROM requests`).Scan(&n)
	if n != 1 {
		t.Fatalf("logged requests = %d, want 1 (the blocked turn never went upstream)", n)
	}
}

func TestWSRejectsFrozenAtHandshake(t *testing.T) {
	h, _, url := wsRig(t)
	if _, err := h.st.FreezeEmployee(h.ctx, h.empID, "test"); err != nil {
		t.Fatal(err)
	}
	_, resp, err := dialGW(t, url, h.key)
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("frozen: want 403 at handshake, got %v %v", resp, err)
	}
	_ = store.ErrNotFound
}
