package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiseat/internal/devicecard"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"

	"github.com/go-chi/chi/v5"
)

type cardRig struct {
	ts    *httptest.Server
	st    *store.Store
	cards *devicecard.Signer
	emp   store.Employee
	dev   store.Device
	ctx   context.Context
}

func newCardRig(t *testing.T, verifyToken string) *cardRig {
	t.Helper()
	st := store.New(testutil.NewDB(t))
	box, _ := secure.NewBox(make([]byte, 32))
	cards := &devicecard.Signer{Box: box}
	srv := web.NewServer(web.Deps{
		Store: st, Settings: settings.NewService(st), Box: box, PublicBaseURL: "https://portal",
		Cards: cards, FeishuVerifyToken: verifyToken,
	})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_owner", Name: "owner"})
	if _, _, err := st.IssueKey(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	dev, _, err := st.EnsureDevice(ctx, store.Device{EmployeeID: emp.ID, DeviceKey: "inst:new", Kind: "codex_install", Label: "MacBook"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetDeviceStatus(ctx, dev.ID, store.DevicePending, nil)
	return &cardRig{ts: ts, st: st, cards: cards, emp: emp, dev: dev, ctx: ctx}
}

func (c *cardRig) post(t *testing.T, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(c.ts.URL+"/api/feishu/card", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_status": resp.StatusCode, "_raw": string(raw)}
	}
	return out
}

func click(token, operator string) string {
	return `{"schema":"2.0","header":{"event_type":"card.action.trigger","token":"vt"},` +
		`"event":{"operator":{"open_id":"` + operator + `"},"action":{"tag":"button","value":{"t":"` + token + `"}}}}`
}

func (c *cardRig) status(t *testing.T) string {
	t.Helper()
	d, err := c.st.GetDevice(c.ctx, c.dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d.Status
}

func TestFeishuCardURLVerificationTurnsCardsOn(t *testing.T) {
	c := newCardRig(t, "")
	if c.st.CardCallbackReady(c.ctx) {
		t.Fatal("ready before Feishu checked the URL")
	}
	out := c.post(t, `{"challenge":"abc123","token":"vt","type":"url_verification"}`)
	if out["challenge"] != "abc123" || !c.st.CardCallbackReady(c.ctx) {
		t.Fatalf("challenge reply %v, ready %v", out, c.st.CardCallbackReady(c.ctx))
	}
}

func TestFeishuCardConfirmAndDeny(t *testing.T) {
	c := newCardRig(t, "")
	now := time.Now()

	// Someone else pressing the owner's button does nothing.
	out := c.post(t, click(c.cards.Token(devicecard.Confirm, c.emp.ID, c.dev.ID, now), "ou_someone_else"))
	if c.status(t) != store.DevicePending || !strings.Contains(toJSON(out), "只能由本人操作") {
		t.Fatalf("other operator: %v, status %s", out, c.status(t))
	}
	// A made-up token does nothing.
	out = c.post(t, click("not-a-real-token", "ou_owner"))
	if c.status(t) != store.DevicePending || !strings.Contains(toJSON(out), "已过期") {
		t.Fatalf("forged token: %v", out)
	}
	// 是我: trusted, and the card is replaced by one without buttons.
	out = c.post(t, click(c.cards.Token(devicecard.Confirm, c.emp.ID, c.dev.ID, now), "ou_owner"))
	if c.status(t) != store.DeviceTrusted || !strings.Contains(toJSON(out), "已确认是你") || strings.Contains(toJSON(out), `"behaviors"`) {
		t.Fatalf("confirm: %v, status %s", out, c.status(t))
	}

	// 不是我 on another device: blocked, key revoked.
	d2, _, _ := c.st.EnsureDevice(c.ctx, store.Device{EmployeeID: c.emp.ID, DeviceKey: "inst:thief", Kind: "codex_install", Label: "thief"})
	c.dev = d2
	out = c.post(t, click(c.cards.Token(devicecard.Deny, c.emp.ID, d2.ID, now), "ou_owner"))
	if c.status(t) != store.DeviceBlocked || !strings.Contains(toJSON(out), "Key 已作废") {
		t.Fatalf("deny: %v, status %s", out, c.status(t))
	}
	if me, _ := c.st.GetEmployee(c.ctx, c.emp.ID); me.ID == 0 {
		t.Fatal("employee gone")
	}
	var active int
	_ = c.st.Pool.QueryRow(c.ctx, `SELECT COUNT(*) FROM api_keys WHERE employee_id=$1 AND revoked_at IS NULL`, c.emp.ID).Scan(&active)
	if active != 0 {
		t.Fatalf("active keys after 不是我 = %d", active)
	}
}

func TestFeishuCardChecksVerificationToken(t *testing.T) {
	c := newCardRig(t, "vt-secret")
	out := c.post(t, `{"challenge":"x","token":"wrong","type":"url_verification"}`)
	if out["challenge"] == "x" || c.st.CardCallbackReady(c.ctx) {
		t.Fatalf("wrong token accepted: %v", out)
	}
	out = c.post(t, `{"challenge":"x","token":"vt-secret","type":"url_verification"}`)
	if out["challenge"] != "x" {
		t.Fatalf("right token refused: %v", out)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
