package web_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"aiseat/internal/secure"
	"aiseat/internal/store"
)

func TestServiceKeysAreIndependentAndAdminOnly(t *testing.T) {
	ts, st, ctx := newServer(t)
	admin, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_key_admin", Name: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSuperAdmin(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "ou_key_admin")
	svc, err := st.CreateServiceAccount(ctx, "service")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/admin/employees/%d", svc.ID)
	var keys []struct {
		Key string `json:"key"`
		ID  int64  `json:"id"`
	}
	for _, label := range []string{"one", "two"} {
		resp, body := c.do(http.MethodPost, path+"/key", fmt.Sprintf(`{"label":%q}`, label))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create: %d %s", resp.StatusCode, body)
		}
		var k struct {
			Key string `json:"key"`
			ID  int64  `json:"id"`
		}
		if err := json.Unmarshal(body, &k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		if _, err := st.LookupKey(ctx, secure.HashAPIKey(k.Key)); err != nil {
			t.Fatalf("key unavailable: %v", err)
		}
	}
	resp, body := c.do(http.MethodGet, path+"/keys", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var listed struct {
		Keys []store.ServiceKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &listed); err != nil || len(listed.Keys) != 2 {
		t.Fatalf("list: %s %v", body, err)
	}
	resp, body = c.do(http.MethodDelete, fmt.Sprintf("%s/keys/%d", path, keys[0].ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke %d %s", resp.StatusCode, body)
	}
	if _, err := st.LookupKey(ctx, secure.HashAPIKey(keys[0].Key)); err != store.ErrNotFound {
		t.Fatalf("revoked key: %v", err)
	}
	if _, err := st.LookupKey(ctx, secure.HashAPIKey(keys[1].Key)); err != nil {
		t.Fatalf("other key: %v", err)
	}
	rows, err := st.ListEmployees(ctx, store.EmployeeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range rows {
		if e.ID == svc.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("service rows: %d", n)
	}
	other := login(t, ts, "ou_nonadmin")
	resp, _ = other.do(http.MethodGet, path+"/keys", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("nonadmin: %d", resp.StatusCode)
	}
}
