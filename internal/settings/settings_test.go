package settings_test

import (
	"context"
	"testing"

	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func newSvc(t *testing.T) (*settings.Service, *store.Store, context.Context) {
	t.Helper()
	pool := testutil.NewDB(t)
	st := store.New(pool)
	return settings.NewService(st), st, context.Background()
}

func TestGetDefaultsWhenEmpty(t *testing.T) {
	svc, _, ctx := newSvc(t)
	s := svc.Get(ctx)
	if s.DefaultMaxConcurrency != 4 {
		t.Fatalf("default concurrency = %d", s.DefaultMaxConcurrency)
	}
	if s.RetentionDays != 180 {
		t.Fatalf("retention = %d", s.RetentionDays)
	}
	if len(s.RedactionRules) == 0 || len(s.ClientRules) == 0 {
		t.Fatal("expected default rules")
	}
	if s.CodexProfile.Originator != "codex-tui" {
		t.Fatalf("originator = %q", s.CodexProfile.Originator)
	}
}

func TestPutAndGetPersists(t *testing.T) {
	svc, st, ctx := newSvc(t)
	in := settings.Defaults()
	in.DefaultMaxConcurrency = 6
	in.QueueTimeoutSeconds = 90
	if err := svc.Put(ctx, in); err != nil {
		t.Fatalf("put: %v", err)
	}
	// A second service on the same store has a cold cache and must read from DB.
	svc2 := settings.NewService(st)
	got := svc2.Get(ctx)
	if got.DefaultMaxConcurrency != 6 || got.QueueTimeoutSeconds != 90 {
		t.Fatalf("got %+v", got)
	}
}

func TestPutRejectsBadValues(t *testing.T) {
	svc, _, ctx := newSvc(t)
	bad := settings.Defaults()
	bad.DefaultMaxConcurrency = 999
	if err := svc.Put(ctx, bad); err == nil {
		t.Fatal("expected error for concurrency 999")
	}
	bad = settings.Defaults()
	bad.RedactionRules = []settings.RedactionRule{{Name: "bad", Pattern: "("}}
	if err := svc.Put(ctx, bad); err == nil {
		t.Fatal("expected error for uncompilable regex")
	}
}

func TestPartialStoredRowMergesDefaults(t *testing.T) {
	_, st, ctx := newSvc(t)
	// Store a row with only one field set; the rest must fall back to defaults.
	if err := st.PutSettingRaw(ctx, "gateway", []byte(`{"queue_timeout_seconds":120}`)); err != nil {
		t.Fatal(err)
	}
	svc2 := settings.NewService(st)
	got := svc2.Get(ctx)
	if got.QueueTimeoutSeconds != 120 {
		t.Fatalf("timeout = %d", got.QueueTimeoutSeconds)
	}
	if got.DefaultMaxConcurrency != 4 {
		t.Fatalf("concurrency should default to 4, got %d", got.DefaultMaxConcurrency)
	}
	if got.CodexProfile.Originator != "codex-tui" {
		t.Fatalf("codex originator should default, got %q", got.CodexProfile.Originator)
	}
}

func TestValidate(t *testing.T) {
	if err := settings.Validate(settings.Defaults()); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

func TestV1RowGetsRiskDefaults(t *testing.T) {
	svc, st, ctx := newSvc(t)
	// A V1 settings row (it still carries the since-removed notice text).
	legacy := `{"notice_text":"本服务的请求和回复会被公司记录，用于用量管理和工作分析，原文保留 180 天。继续使用即表示你已知晓。"}`
	if err := st.PutSettingRaw(ctx, "gateway", []byte(legacy)); err != nil {
		t.Fatal(err)
	}
	s := svc.Get(ctx)
	if s.RiskMode != settings.RiskStandard || s.AllowNonCodex {
		t.Fatalf("risk defaults not applied to a V1 row: %+v", s)
	}
}

func TestEmptyAuditPatternRejected(t *testing.T) {
	s := settings.Defaults()
	s.ContentAuditRules = append(s.ContentAuditRules, settings.ContentRule{Name: "oops", Pattern: "  "})
	if err := settings.Validate(s); err == nil {
		t.Fatal("an empty pattern would match every message and must be rejected")
	}
}
