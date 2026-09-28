package proxy

import (
	"context"
	"errors"
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
