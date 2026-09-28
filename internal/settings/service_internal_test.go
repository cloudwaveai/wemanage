package settings

import (
	"context"
	"testing"
	"time"

	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestServiceKeepsLastGoodSettingsWhenDBDown(t *testing.T) {
	pool := testutil.NewDB(t)
	svc := NewService(store.New(pool))
	svc.ttl = time.Millisecond
	ctx := context.Background()

	s := Defaults()
	s.DefaultMaxConcurrency = 2
	s.RiskMode = RiskStrict
	if err := svc.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	time.Sleep(5 * time.Millisecond)

	got := svc.Get(ctx)
	if got.DefaultMaxConcurrency != 2 || got.RiskMode != RiskStrict {
		t.Fatalf("settings reverted to defaults while DB down: %+v", got)
	}
}
