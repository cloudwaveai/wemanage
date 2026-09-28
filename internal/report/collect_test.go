package report_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiseat/internal/report"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
)

func TestCollect(t *testing.T) {
	pool := testutil.NewDB(t)
	st := store.New(pool)
	ctx := context.Background()

	emp, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_collect", Name: "Alice"})
	if err != nil {
		t.Fatalf("upsert employee: %v", err)
	}

	loc := time.UTC
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, loc)
	t9 := day.Add(9 * time.Hour)
	t14 := day.Add(14 * time.Hour)

	sess1 := int64(101)
	sess2 := int64(202)

	id1 := [16]byte{1}
	id2 := [16]byte{2}

	// Request 1 in session 1 at 09:00.
	if err := st.InsertRequest(ctx, store.RequestRow{
		ID: id1, StartedAt: t9, EmployeeID: emp.ID, SessionID: &sess1,
		Endpoint: "responses", StatusCode: 200,
		TokensInput: 10, TokensOutput: 5, TokensReasoning: 2,
		ClientKind: "codex", ModelRequested: "gpt-5.1-codex",
	}); err != nil {
		t.Fatalf("insert request 1: %v", err)
	}
	// Request 2 in session 2 at 14:00.
	if err := st.InsertRequest(ctx, store.RequestRow{
		ID: id2, StartedAt: t14, EmployeeID: emp.ID, SessionID: &sess2,
		Endpoint: "responses", StatusCode: 200,
		TokensInput: 20, TokensOutput: 7, TokensReasoning: 3,
		ClientKind: "codex", ModelRequested: "gpt-5.1-codex",
	}); err != nil {
		t.Fatalf("insert request 2: %v", err)
	}

	longText := strings.Repeat("あ", 2000) // 2000 runes, must truncate to 1500

	if err := st.InsertTexts(ctx, []store.TextRow{
		{RequestID: id1, EmployeeID: emp.ID, SessionID: &sess1, StartedAt: t9, Role: "user", Text: "帮我修个 bug"},
		{RequestID: id1, EmployeeID: emp.ID, SessionID: &sess1, StartedAt: t9.Add(time.Second), Role: "assistant", Text: "已修复"},
		{RequestID: id2, EmployeeID: emp.ID, SessionID: &sess2, StartedAt: t14, Role: "user", Text: longText},
	}); err != nil {
		t.Fatalf("insert texts: %v", err)
	}

	// Noise: another employee's request in the window must not be counted.
	other, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_other", Name: "Bob"})
	if err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	if err := st.InsertRequest(ctx, store.RequestRow{
		ID: [16]byte{9}, StartedAt: t9, EmployeeID: other.ID,
		Endpoint: "responses", StatusCode: 200, TokensInput: 999,
		ClientKind: "codex", ModelRequested: "gpt-5.1-codex",
	}); err != nil {
		t.Fatalf("insert other request: %v", err)
	}

	mat, err := report.Collect(ctx, st, emp.ID, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if mat.Stats.Requests != 2 {
		t.Errorf("Requests = %d, want 2", mat.Stats.Requests)
	}
	if mat.Stats.TokensInput != 30 {
		t.Errorf("TokensInput = %d, want 30", mat.Stats.TokensInput)
	}
	if mat.Stats.TokensOutput != 12 {
		t.Errorf("TokensOutput = %d, want 12", mat.Stats.TokensOutput)
	}
	if mat.Stats.TokensReasoning != 5 {
		t.Errorf("TokensReasoning = %d, want 5", mat.Stats.TokensReasoning)
	}

	if len(mat.Sessions) != 2 {
		t.Fatalf("Sessions = %d, want 2", len(mat.Sessions))
	}
	// Ordered by session_id ascending.
	if mat.Sessions[0].SessionID != sess1 {
		t.Errorf("Sessions[0].SessionID = %d, want %d", mat.Sessions[0].SessionID, sess1)
	}
	if len(mat.Sessions[0].Texts) != 2 {
		t.Errorf("session 1 turns = %d, want 2", len(mat.Sessions[0].Texts))
	}
	if mat.Sessions[1].SessionID != sess2 {
		t.Errorf("Sessions[1].SessionID = %d, want %d", mat.Sessions[1].SessionID, sess2)
	}
	if len(mat.Sessions[1].Texts) != 1 {
		t.Fatalf("session 2 turns = %d, want 1", len(mat.Sessions[1].Texts))
	}
	// Chronological order within session 1: user before assistant.
	if mat.Sessions[0].Texts[0].Role != "user" || mat.Sessions[0].Texts[1].Role != "assistant" {
		t.Errorf("session 1 turn order = [%s,%s], want [user,assistant]",
			mat.Sessions[0].Texts[0].Role, mat.Sessions[0].Texts[1].Role)
	}
	// Long text truncated to 1500 runes (rune-safe, not byte-cut).
	got := mat.Sessions[1].Texts[0].Text
	if n := len([]rune(got)); n != 1500 {
		t.Errorf("truncated rune count = %d, want 1500", n)
	}
}
