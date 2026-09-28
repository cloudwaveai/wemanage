package proxy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestPrincipalCacheServesStaleWhenDBDown(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()
	e, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_s", Name: "s"})
	plain, _, err := st.IssueKey(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := newPrincipalCache(st, time.Millisecond)

	if _, err := c.Get(ctx, secure.HashAPIKey(plain)); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if _, err := c.Get(ctx, secure.HashAPIKey("sk-emp-nope")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown key: err = %v, want ErrNotFound", err)
	}

	pool.Close() // the (cross-border) database link goes away
	time.Sleep(5 * time.Millisecond)

	p, err := c.Get(ctx, secure.HashAPIKey(plain))
	if err != nil || p.EmployeeID != e.ID {
		t.Fatalf("known key while DB down: p=%+v err=%v, want stale principal", p, err)
	}
	if _, err := c.Get(ctx, secure.HashAPIKey("sk-emp-other")); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("uncached key while DB down: err = %v, want a DB error (not ErrNotFound)", err)
	}
}

func TestPrincipalCacheRefreshesInBackground(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()
	e, _ := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_r", Name: "r"})
	plain, _, _ := st.IssueKey(ctx, e.ID)
	c := newPrincipalCache(st, 20*time.Millisecond)
	hash := secure.HashAPIKey(plain)
	if _, err := c.Get(ctx, hash); err != nil {
		t.Fatal(err)
	}

	_ = st.RevokeKeys(ctx, e.ID)
	time.Sleep(30 * time.Millisecond) // past ttl

	// Served immediately from cache (no request waits on the database)...
	if _, err := c.Get(ctx, hash); err != nil {
		t.Fatalf("stale entry should be served while refreshing: %v", err)
	}
	// ...and the background refresh picks up the revocation.
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := c.Get(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revocation not picked up; last err = %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBookkeepingWritesAreCoalesced(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	h := NewHandler(HandlerDeps{Store: st})
	hdr := http.Header{}
	hdr.Set("User-Agent", "codex-tui/0.157.1 (Linux; x64) x (codex-tui; 0.157.1)")
	for i := 0; i < 5; i++ {
		h.recordObserved(hdr)
	}
	var hits int64
	if err := pool.QueryRow(context.Background(), `SELECT hits FROM observed_clients`).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 1 || h.observed[hdr.Get("User-Agent")].pending != 4 {
		t.Fatalf("db hits = %d, pending = %d; want 1 written and 4 held", hits, h.observed[hdr.Get("User-Agent")].pending)
	}

	if !h.quotaDue(7, 200) || h.quotaDue(7, 200) {
		t.Fatal("quota snapshot should be written once per minute per account")
	}
	if !h.quotaDue(7, http.StatusTooManyRequests) {
		t.Fatal("a rate-limit response must always record quota")
	}
}
