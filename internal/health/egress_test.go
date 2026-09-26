package health_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiseat/internal/health"
	"aiseat/internal/store"
)

func TestCheckGroupDirect(t *testing.T) {
	const egress = "203.0.113.7"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("  " + egress + "\n"))
	}))
	defer srv.Close()

	// Empty ProxyURL -> direct request; body is returned trimmed.
	ip, err := health.CheckGroup(context.Background(), store.IPGroup{ProxyURL: ""}, srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("check group: %v", err)
	}
	if ip != egress {
		t.Fatalf("ip = %q, want %q", ip, egress)
	}
}

func TestCheckAllFiresOnChange(t *testing.T) {
	st, ctx := newStore(t)
	box := newBox(t)

	const egress = "198.51.100.42"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(egress))
	}))
	defer srv.Close()

	// Group with a direct (empty) proxy but a stale ExpectedEgressIP so the
	// observed IP differs -> onChange must fire.
	g, err := st.CreateIPGroup(ctx, box, "changed", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordEgressCheck(ctx, g.ID, "10.0.0.1", ""); err != nil {
		t.Fatal(err)
	}
	// The first RecordEgressCheck seeded ExpectedEgressIP to 10.0.0.1 (it was
	// empty). Confirm that so the change path is exercised.
	groups, err := st.ListIPGroups(ctx, box)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ExpectedEgressIP != "10.0.0.1" {
		t.Fatalf("expected seeded egress 10.0.0.1, got %+v", groups)
	}

	var gotGroup int64
	var gotOld, gotNew string
	calls := 0
	onChange := func(groupID int64, oldIP, newIP string) {
		calls++
		gotGroup, gotOld, gotNew = groupID, oldIP, newIP
	}
	if err := health.CheckAll(ctx, st, box, srv.URL, 5*time.Second, onChange); err != nil {
		t.Fatalf("check all: %v", err)
	}
	if calls != 1 {
		t.Fatalf("onChange calls = %d, want 1", calls)
	}
	if gotGroup != g.ID || gotOld != "10.0.0.1" || gotNew != egress {
		t.Fatalf("onChange args = (%d,%q,%q)", gotGroup, gotOld, gotNew)
	}

	// After CheckAll recorded the new IP, expected stays 10.0.0.1 (not overwritten),
	// but last egress is updated to the observed value.
	groups, _ = st.ListIPGroups(ctx, box)
	if groups[0].LastEgressIP != egress {
		t.Fatalf("last egress = %q, want %q", groups[0].LastEgressIP, egress)
	}

	// A matching expected IP does not fire onChange.
	g2, err := st.CreateIPGroup(ctx, box, "stable", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordEgressCheck(ctx, g2.ID, egress, ""); err != nil {
		t.Fatal(err)
	}
	calls = 0
	// Only re-check g2 by pointing CheckAll at a server returning the same IP;
	// g (id=1) will still differ, so filter by counting g2 changes explicitly.
	seen := map[int64]bool{}
	if err := health.CheckAll(ctx, st, box, srv.URL, 5*time.Second, func(id int64, o, n string) {
		seen[id] = true
	}); err != nil {
		t.Fatal(err)
	}
	if seen[g2.ID] {
		t.Fatal("stable group should not fire onChange")
	}
}
