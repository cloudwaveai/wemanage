package web_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

func TestDeviceConfirmAndDeny(t *testing.T) {
	ts, st, ctx := newServer(t)
	c := login(t, ts, "ou_dev")
	emp, _ := st.GetEmployeeByOpenID(ctx, "ou_dev")
	_ = st.AckNotice(ctx, emp.ID, settings.Defaults().NoticeVersion)
	if _, _, err := st.IssueKey(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	mine, _, _ := st.EnsureDevice(ctx, store.Device{EmployeeID: emp.ID, DeviceKey: "inst:mine", Kind: "codex_install", Label: "mine"})
	theirs, _, _ := st.EnsureDevice(ctx, store.Device{EmployeeID: emp.ID, DeviceKey: "inst:theirs", Kind: "codex_install", Label: "theirs"})

	// /me reports two devices awaiting confirmation.
	_, body := c.do(http.MethodGet, "/api/me", "")
	var me struct {
		PendingDevices int  `json:"pending_devices"`
		Frozen         bool `json:"frozen"`
	}
	_ = json.Unmarshal(body, &me)
	if me.PendingDevices != 2 || me.Frozen {
		t.Fatalf("me: %s", body)
	}

	resp, _ := c.do(http.MethodPost, "/api/me/devices/"+itoa(mine.ID)+"/confirm", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("confirm status %d", resp.StatusCode)
	}
	if d, _ := st.GetDevice(ctx, mine.ID); d.Status != store.DeviceTrusted {
		t.Fatalf("status after confirm = %s", d.Status)
	}

	resp, body = c.do(http.MethodPost, "/api/me/devices/"+itoa(theirs.ID)+"/deny", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("deny status %d: %s", resp.StatusCode, body)
	}
	if d, _ := st.GetDevice(ctx, theirs.ID); d.Status != store.DeviceBlocked {
		t.Fatalf("status after deny = %s", d.Status)
	}
	if _, err := st.ActiveKey(ctx, emp.ID); err == nil {
		t.Fatal("deny should revoke the active key")
	}
	evs, _ := st.ListRiskEvents(ctx, store.RiskEventFilter{EmployeeID: &emp.ID})
	if len(evs) != 1 || evs[0].Rule != "device_denied" || evs[0].Severity != "high" {
		t.Fatalf("events: %+v", evs)
	}
	// A blocked device cannot be re-confirmed by the employee.
	resp, _ = c.do(http.MethodPost, "/api/me/devices/"+itoa(theirs.ID)+"/confirm", "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("confirm blocked device: status %d, want 403", resp.StatusCode)
	}
}

func TestDeviceOwnership(t *testing.T) {
	ts, st, ctx := newServer(t)
	other, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_other", Name: "other"})
	d, _, _ := st.EnsureDevice(ctx, store.Device{EmployeeID: other.ID, DeviceKey: "k", Kind: "ua"})
	c := login(t, ts, "ou_snoop")
	resp, _ := c.do(http.MethodPost, "/api/me/devices/"+itoa(d.ID)+"/confirm", "{}")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("confirming someone else's device: status %d, want 404", resp.StatusCode)
	}
}

func TestFrozenCannotReissueKeyUntilAdminUnfreezes(t *testing.T) {
	ts, st, ctx := newServer(t)
	c := login(t, ts, "ou_frz")
	emp, _ := st.GetEmployeeByOpenID(ctx, "ou_frz")
	c.do(http.MethodPost, "/api/me/ack-notice", "{}")
	if _, err := st.FreezeEmployee(ctx, emp.ID, "test"); err != nil {
		t.Fatal(err)
	}
	resp, _ := c.do(http.MethodPost, "/api/me/key", "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("frozen key issue: status %d, want 403", resp.StatusCode)
	}

	// A plain employee cannot unfreeze themselves.
	resp, _ = c.do(http.MethodPost, "/api/admin/employees/"+itoa(emp.ID)+"/unfreeze", "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self unfreeze: status %d, want 403", resp.StatusCode)
	}

	if err := st.EnsureSuperAdmin(ctx, "ou_boss", "boss"); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "ou_boss")
	resp, _ = admin.do(http.MethodPost, "/api/admin/employees/"+itoa(emp.ID)+"/unfreeze", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("admin unfreeze: status %d", resp.StatusCode)
	}
	resp, _ = c.do(http.MethodPost, "/api/me/key", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("key after unfreeze: status %d", resp.StatusCode)
	}
	resp, body := admin.do(http.MethodGet, "/api/admin/risk/events", "")
	if resp.StatusCode != 200 {
		t.Fatalf("risk events: status %d %s", resp.StatusCode, body)
	}
}

func TestNoticeReackAfterTextChange(t *testing.T) {
	ts, st, ctx := newServer(t)
	c := login(t, ts, "ou_re")
	c.do(http.MethodPost, "/api/me/ack-notice", "{}")

	set := settings.NewService(st)
	s := set.Get(ctx)
	s.NoticeText = "新的告知内容"
	if err := set.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	// The server's own settings cache may lag up to its TTL; read through the API.
	check := func() bool {
		_, body := c.do(http.MethodGet, "/api/me", "")
		var me struct {
			NoticeAcked bool `json:"notice_acked"`
		}
		_ = json.Unmarshal(body, &me)
		return me.NoticeAcked
	}
	waitFor(t, func() bool { return !check() })
	c.do(http.MethodPost, "/api/me/ack-notice", "{}")
	if !check() {
		t.Fatal("re-ack should satisfy the new notice version")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
