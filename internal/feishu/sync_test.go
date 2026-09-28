package feishu_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiseat/internal/feishu"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

// syncFakeServer serves one page of departments and each department's users.
// With emptyUsers, every department is empty.
func syncFakeServer(t *testing.T, emptyUsers bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"code": 0, "tenant_access_token": "tok", "expire": 7200})
		})
	mux.HandleFunc("/open-apis/contact/v3/departments/0/children",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{"open_department_id": "od-root", "parent_department_id": "0", "name": "Root",
							"leaders": []map[string]any{{"leader_type": 1, "leader_id": "ou_leader"}}},
						{"open_department_id": "od-eng", "parent_department_id": "od-root", "name": "Eng"},
					},
					"page_token": "",
					"has_more":   false,
				},
			})
		})
	mux.HandleFunc("/open-apis/contact/v3/users/find_by_department",
		func(w http.ResponseWriter, r *http.Request) {
			var items []map[string]any
			if !emptyUsers {
				switch r.URL.Query().Get("department_id") {
				case "od-eng":
					items = []map[string]any{
						{"open_id": "ou_alice", "union_id": "on_alice", "user_id": "u_alice",
							"name": "Alice", "email": "alice@x.com", "avatar": map[string]any{"avatar_72": "http://img/a.png"},
							"department_ids": []string{"od-eng"}},
					}
				case "od-root":
					items = []map[string]any{
						{"open_id": "ou_bob", "name": "Bob",
							"status": map[string]any{"is_resigned": true}, "department_ids": []string{"od-unknown"}},
					}
				}
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"items": items, "page_token": "", "has_more": false}})
		})
	return httptest.NewServer(mux)
}

func TestRunSyncEmptyDirectoryDisablesNobody(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()
	if _, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_keep", Name: "Keep"}); err != nil {
		t.Fatal(err)
	}
	srv := syncFakeServer(t, true)
	defer srv.Close()
	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())

	if _, _, err := feishu.Run(ctx, st, cl); !errors.Is(err, feishu.ErrEmptyDirectory) {
		t.Fatalf("err = %v, want ErrEmptyDirectory", err)
	}
	keep, _ := st.GetEmployeeByOpenID(ctx, "ou_keep")
	if keep.Status != "active" {
		t.Fatalf("empty feed must not disable anyone; status = %q", keep.Status)
	}
}

func TestRunSync(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()

	// Seed an employee that the feed will NOT include; Run should disable it.
	stale, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_stale", Name: "Stale"})
	if err != nil {
		t.Fatalf("seed stale employee: %v", err)
	}
	if stale.Status != "active" {
		t.Fatalf("seeded employee status = %q, want active", stale.Status)
	}

	srv := syncFakeServer(t, false)
	defer srv.Close()
	cl := feishu.NewClient(srv.URL, "app", "secret", srv.Client())

	deptCount, userCount, err := feishu.Run(ctx, st, cl)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if deptCount != 2 || userCount != 2 {
		t.Fatalf("counts = %d depts / %d users, want 2/2", deptCount, userCount)
	}

	// Departments persisted.
	depts, err := st.ListDepartments(ctx)
	if err != nil {
		t.Fatalf("list departments: %v", err)
	}
	if len(depts) != 2 {
		t.Fatalf("got %d departments, want 2", len(depts))
	}

	// Alice active, Bob disabled (feed said resigned).
	alice, err := st.GetEmployeeByOpenID(ctx, "ou_alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice.Status != "active" || alice.Name != "Alice" {
		t.Fatalf("alice = %+v, want active/Alice", alice)
	}
	bob, err := st.GetEmployeeByOpenID(ctx, "ou_bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob.Status != "disabled" {
		t.Fatalf("bob status = %q, want disabled", bob.Status)
	}

	// The stale employee, absent from the feed, is disabled.
	staleAfter, err := st.GetEmployeeByOpenID(ctx, "ou_stale")
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if staleAfter.Status != "disabled" {
		t.Fatalf("stale status = %q, want disabled", staleAfter.Status)
	}

	// Alice's department link resolved to "Eng"; Bob's unknown dept skipped.
	rows, err := st.ListEmployees(ctx, store.EmployeeFilter{})
	if err != nil {
		t.Fatalf("list employees: %v", err)
	}
	var aliceDepts, bobDepts []string
	for _, r := range rows {
		switch r.FeishuOpenID {
		case "ou_alice":
			aliceDepts = r.DepartmentNames
		case "ou_bob":
			bobDepts = r.DepartmentNames
		}
	}
	if len(aliceDepts) != 1 || aliceDepts[0] != "Eng" {
		t.Fatalf("alice departments = %v, want [Eng]", aliceDepts)
	}
	if len(bobDepts) != 0 {
		t.Fatalf("bob departments = %v, want none (unknown dept skipped)", bobDepts)
	}

	// Audit record written with the counts.
	audits, err := st.ListAudit(ctx, store.AuditFilter{})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var found *store.AuditRow
	for i := range audits {
		if audits[i].Action == "feishu_sync" {
			found = &audits[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no feishu_sync audit record found")
	}
	if got := jsonNum(found.Detail["departments"]); got != 2 {
		t.Fatalf("audit departments = %v, want 2", found.Detail["departments"])
	}
	if got := jsonNum(found.Detail["users"]); got != 2 {
		t.Fatalf("audit users = %v, want 2", found.Detail["users"])
	}
	if got := jsonNum(found.Detail["disabled"]); got != 1 {
		t.Fatalf("audit disabled = %v, want 1", found.Detail["disabled"])
	}
}

// jsonNum coerces a JSON-decoded numeric (float64 or json.Number) to int64.
func jsonNum(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return -1
	}
}
