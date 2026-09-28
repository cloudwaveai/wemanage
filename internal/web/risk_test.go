package web_test

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"aiseat/internal/store"
)

func TestDeviceConfirmAndDeny(t *testing.T) {
	ts, st, ctx := newServer(t)
	c := login(t, ts, "ou_dev")
	emp, _ := st.GetEmployeeByOpenID(ctx, "ou_dev")
	if _, _, err := st.IssueKey(ctx, emp.ID); err != nil {
		t.Fatal(err)
	}
	// The first device is trusted automatically; the two after it wait.
	_, _, _ = st.EnsureDevice(ctx, store.Device{EmployeeID: emp.ID, DeviceKey: "inst:first", Kind: "codex_install", Label: "first"})
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

func TestTeamUsageScope(t *testing.T) {
	ts, st, ctx := newServer(t)
	leader, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_tl", Name: "leader"})
	sub, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_ts", Name: "sub"})
	out, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_to", Name: "outsider"})
	d, _ := st.UpsertDepartment(ctx, store.Department{FeishuDeptID: "td", Name: "D", LeaderOpenIDs: []string{"ou_tl"}})
	_ = st.SetEmployeeDepartments(ctx, leader.ID, []int64{d})
	_ = st.SetEmployeeDepartments(ctx, sub.ID, []int64{d})
	for _, id := range []int64{sub.ID, out.ID} {
		if err := st.InsertRequest(ctx, store.RequestRow{ID: newUUID(), StartedAt: time.Now(), EmployeeID: id,
			ClientKind: "codex", Endpoint: "responses", StatusCode: 200, TokensInput: 10}); err != nil {
			t.Fatal(err)
		}
	}

	resp, body := login(t, ts, "ou_tl").do(http.MethodGet, "/api/team/usage", "")
	if resp.StatusCode != 200 {
		t.Fatalf("leader team usage: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Summary struct{ Requests int64 }
		Members []struct {
			EmployeeID int64 `json:"employee_id"`
		}
	}
	_ = json.Unmarshal(body, &got)
	ids := map[int64]bool{}
	for _, m := range got.Members {
		ids[m.EmployeeID] = true
	}
	if ids[out.ID] || !ids[sub.ID] || got.Summary.Requests != 1 {
		t.Fatalf("leader must see only their department: %s", body)
	}
	// Team totals only: no per-person usage or activity times.
	var raw struct {
		Members []map[string]any `json:"members"`
	}
	_ = json.Unmarshal(body, &raw)
	for _, m := range raw.Members {
		for _, field := range []string{"requests", "tokens_input", "last_active", "frozen", "quota"} {
			if _, ok := m[field]; ok {
				t.Fatalf("member exposes %s: %v", field, m)
			}
		}
	}

	resp, _ = login(t, ts, "ou_to").do(http.MethodGet, "/api/team/usage", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain employee: status %d, want 403", resp.StatusCode)
	}
}

func TestRemoteAllowedToggleIsAdminOnly(t *testing.T) {
	ts, st, ctx := newServer(t)
	emp, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_rm", Name: "rm"})
	path := "/api/admin/employees/" + itoa(emp.ID) + "/remote"
	if resp, _ := login(t, ts, "ou_rm").do(http.MethodPost, path, `{"allowed":true}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self toggle: status %d, want 403", resp.StatusCode)
	}
	_ = st.EnsureSuperAdmin(ctx, "ou_boss2", "boss")
	if resp, body := login(t, ts, "ou_boss2").do(http.MethodPost, path, `{"allowed":true}`); resp.StatusCode != 200 {
		t.Fatalf("admin toggle: %d %s", resp.StatusCode, body)
	}
	if e, _ := st.GetEmployee(ctx, emp.ID); !e.RemoteAllowed {
		t.Fatal("remote_allowed not set")
	}
}

func newUUID() [16]byte {
	var u [16]byte
	_, _ = rand.Read(u[:])
	return u
}
