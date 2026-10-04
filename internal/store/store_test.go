package store_test

import (
	"context"
	"testing"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	pool := testutil.NewDB(t)
	return store.New(pool), context.Background()
}

func newBox(t *testing.T) *secure.Box {
	t.Helper()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func mkEmployee(t *testing.T, s *store.Store, ctx context.Context, openID, name string) store.Employee {
	t.Helper()
	e, err := s.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: openID, Name: name})
	if err != nil {
		t.Fatalf("upsert employee: %v", err)
	}
	return e
}

func mkGroup(t *testing.T, s *store.Store, ctx context.Context, box *secure.Box, name, proxy string) store.IPGroup {
	t.Helper()
	g, err := s.CreateIPGroup(ctx, box, name, proxy, 10)
	if err != nil {
		t.Fatalf("create ip group: %v", err)
	}
	return g
}

func mkAccount(t *testing.T, s *store.Store, ctx context.Context, groupID int64, cpaName string) store.Account {
	t.Helper()
	a, err := s.CreateAccount(ctx, store.Account{
		CPAAuthName:   cpaName,
		IPGroupID:     groupID,
		DeviceProfile: store.DeviceProfile{OS: "Mac OS", InstallationID: "iid-1"},
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return a
}

func TestIssueKeyRevokesPrevious(t *testing.T) {
	s, ctx := newStore(t)
	e := mkEmployee(t, s, ctx, "ou_a", "alice")

	plain1, _, err := s.IssueKey(ctx, e.ID)
	if err != nil {
		t.Fatalf("issue 1: %v", err)
	}
	// First key resolves.
	if _, err := s.LookupKey(ctx, secure.HashAPIKey(plain1)); err != nil {
		t.Fatalf("lookup key1: %v", err)
	}
	plain2, _, err := s.IssueKey(ctx, e.ID)
	if err != nil {
		t.Fatalf("issue 2: %v", err)
	}
	// Old key is now revoked.
	if _, err := s.LookupKey(ctx, secure.HashAPIKey(plain1)); err != store.ErrNotFound {
		t.Fatalf("old key should be revoked, got %v", err)
	}
	if _, err := s.LookupKey(ctx, secure.HashAPIKey(plain2)); err != nil {
		t.Fatalf("new key should resolve: %v", err)
	}
}

func TestLookupKeyPrincipal(t *testing.T) {
	s, ctx := newStore(t)
	box := newBox(t)
	e := mkEmployee(t, s, ctx, "ou_b", "bob")
	g := mkGroup(t, s, ctx, box, "grp1", "socks5://1.1.1.1:1080")
	a := mkAccount(t, s, ctx, g.ID, "codex-b.json")

	plain, _, _ := s.IssueKey(ctx, e.ID)
	kp, err := s.LookupKey(ctx, secure.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	if len(kp.Accounts) != 0 {
		t.Fatal("no accounts before binding")
	}
	if err := s.BindAccount(ctx, a.ID, e.ID, 3); err != nil {
		t.Fatalf("bind: %v", err)
	}
	b := mkAccount(t, s, ctx, g.ID, "codex-b2.json")
	if err := s.BindAccount(ctx, b.ID, e.ID, 3); err != nil {
		t.Fatalf("bind second: %v", err)
	}
	kp, err = s.LookupKey(ctx, secure.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	if len(kp.Accounts) != 2 || kp.Accounts[0].ID != a.ID || kp.Accounts[1].ID != b.ID {
		t.Fatalf("expected both accounts, oldest binding first, got %+v", kp.Accounts)
	}
	if acc, ok := kp.Account(a.ID); !ok || acc.Prefix() != "acct-"+itoa(a.ID) {
		t.Fatalf("account %d: %+v %v", a.ID, acc, ok)
	}
}

func TestBindMembers(t *testing.T) {
	s, ctx := newStore(t)
	box := newBox(t)
	g := mkGroup(t, s, ctx, box, "grp", "socks5://1.1.1.1:1080")
	e1 := mkEmployee(t, s, ctx, "ou_1", "e1")
	e2 := mkEmployee(t, s, ctx, "ou_2", "e2")
	e3 := mkEmployee(t, s, ctx, "ou_3", "e3")
	a1 := mkAccount(t, s, ctx, g.ID, "codex-1.json")
	a2 := mkAccount(t, s, ctx, g.ID, "codex-2.json")

	// An account is shared by up to max employees.
	for _, e := range []int64{e1.ID, e2.ID} {
		if err := s.BindAccount(ctx, a1.ID, e, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BindAccount(ctx, a1.ID, e3.ID, 2); err != store.ErrAccountFull {
		t.Fatalf("third member over a cap of 2: %v", err)
	}
	// Rebinding a member is a no-op, even at the cap.
	if err := s.BindAccount(ctx, a1.ID, e1.ID, 2); err != nil {
		t.Fatalf("idempotent rebind: %v", err)
	}
	// An employee can have several accounts.
	if err := s.BindAccount(ctx, a2.ID, e1.ID, 2); err != nil {
		t.Fatalf("second account: %v", err)
	}
	if accs, _ := s.AccountsOf(ctx, e1.ID); len(accs) != 2 {
		t.Fatalf("e1 accounts = %d", len(accs))
	}
	// Removing one member makes room.
	if err := s.UnbindAccount(ctx, a1.ID, e2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindAccount(ctx, a1.ID, e3.ID, 2); err != nil {
		t.Fatalf("after unbind: %v", err)
	}
	rows, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows[0].Members) != 2 || rows[0].Members[0].Name != "e1" || rows[0].Members[1].Name != "e3" {
		t.Fatalf("members = %+v", rows[0].Members)
	}
	if err := s.BindAccount(ctx, 999999, e1.ID, 2); err != store.ErrNotFound {
		t.Fatalf("unknown account: %v", err)
	}
}

func TestAccountModels(t *testing.T) {
	s, ctx := newStore(t)
	g := mkGroup(t, s, ctx, newBox(t), "grp", "socks5://1.1.1.1:1080")
	a := mkAccount(t, s, ctx, g.ID, "codex-m.json")
	if !a.AllowsModel("gpt-6-astra") {
		t.Fatal("a new account allows every model")
	}
	if err := s.SetAccountModels(ctx, a.ID, []string{"gpt-6-luna", "gpt-6-sol"}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAccount(ctx, a.ID)
	if a.AllowsModel("gpt-6-astra") || !a.AllowsModel("gpt-6-sol") || !a.AllowsModel("") {
		t.Fatalf("allowed = %v", a.AllowedModels)
	}
	if err := s.SetAccountModels(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.GetAccount(ctx, a.ID); a.AllowedModels != nil {
		t.Fatalf("cleared policy = %v", a.AllowedModels)
	}
	on := true
	if err := s.UpdateAccount(ctx, a.ID, store.AccountPatch{AllowThirdParty: &on}); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.GetAccount(ctx, a.ID); !a.AllowThirdParty {
		t.Fatal("third-party clients should be allowed")
	}
}

func TestIPGroupProxyEncrypted(t *testing.T) {
	s, ctx := newStore(t)
	box := newBox(t)
	proxy := "socks5://secret-user:secret-pass@10.0.0.9:1080"
	g := mkGroup(t, s, ctx, box, "enc", proxy)

	// Raw column must not contain the plaintext.
	var raw []byte
	if err := s.Pool.QueryRow(ctx, `SELECT proxy_url_enc FROM ip_groups WHERE id=$1`, g.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) == proxy || contains(raw, "secret-pass") {
		t.Fatal("proxy stored in plaintext")
	}
	got, err := s.GetIPGroup(ctx, box, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProxyURL != proxy {
		t.Fatalf("decrypted proxy = %q", got.ProxyURL)
	}
	if got.AccountCount != 0 {
		t.Fatalf("account count = %d", got.AccountCount)
	}
}

func TestDisableEmployeesNotIn(t *testing.T) {
	s, ctx := newStore(t)
	mkEmployee(t, s, ctx, "ou_keep", "keep")
	mkEmployee(t, s, ctx, "ou_gone", "gone")
	n, err := s.DisableEmployeesNotIn(ctx, []string{"ou_keep"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("disabled = %d, want 1", n)
	}
	gone, _ := s.GetEmployeeByOpenID(ctx, "ou_gone")
	if gone.Status != "disabled" {
		t.Fatalf("gone status = %q", gone.Status)
	}
	keep, _ := s.GetEmployeeByOpenID(ctx, "ou_keep")
	if keep.Status != "active" {
		t.Fatalf("keep status = %q", keep.Status)
	}
}

func TestSignals(t *testing.T) {
	s, ctx := newStore(t)
	box := newBox(t)
	g := mkGroup(t, s, ctx, box, "g", "socks5://1.1.1.1:1080")
	a := mkAccount(t, s, ctx, g.ID, "codex.json")

	if _, err := s.AddSignal(ctx, a.ID, "model_mismatch", map[string]any{"req": "gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
	has, _ := s.HasOpenSignal(ctx, a.ID, "model_mismatch")
	if !has {
		t.Fatal("expected open signal")
	}
	n, _ := s.CountSignalsSince(ctx, a.ID, "model_mismatch", time.Now().Add(-time.Hour))
	if n != 1 {
		t.Fatalf("count = %d", n)
	}
	if err := s.ResolveSignalsOfKind(ctx, a.ID, "model_mismatch"); err != nil {
		t.Fatal(err)
	}
	has, _ = s.HasOpenSignal(ctx, a.ID, "model_mismatch")
	if has {
		t.Fatal("signal should be resolved")
	}
}

func TestListEmployeesSummary(t *testing.T) {
	s, ctx := newStore(t)
	box := newBox(t)
	e := mkEmployee(t, s, ctx, "ou_x", "xavier")
	g := mkGroup(t, s, ctx, box, "gx", "socks5://1.1.1.1:1080")
	a := mkAccount(t, s, ctx, g.ID, "cx.json")
	_ = s.BindAccount(ctx, a.ID, e.ID, 3)
	_, _, _ = s.IssueKey(ctx, e.ID)

	rows, err := s.ListEmployees(ctx, store.EmployeeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if len(rows[0].Accounts) != 1 || rows[0].Accounts[0].ID != a.ID || rows[0].Accounts[0].Status != "active" {
		t.Fatalf("accounts = %+v", rows[0].Accounts)
	}
	if rows[0].ActiveKeyPrefix == "" {
		t.Fatal("expected active key prefix")
	}
}

func contains(b []byte, sub string) bool {
	return len(sub) > 0 && string(b) != "" && indexOf(string(b), sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// itoa mirrors the store's internal helper for test assertions.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
