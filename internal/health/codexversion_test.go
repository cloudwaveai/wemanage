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

func TestGitHubLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/openai/codex/releases/latest" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"rust-v0.157.1","name":"0.157.1"}`))
	}))
	defer srv.Close()

	gh := health.GitHub{Base: srv.URL, HC: srv.Client()}
	v, err := gh.Latest(context.Background())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if v != "0.157.1" {
		t.Fatalf("version = %q, want 0.157.1", v)
	}
}

func TestGitHubLatestStripsPlainV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer srv.Close()

	gh := health.GitHub{Base: srv.URL, HC: srv.Client()}
	v, err := gh.Latest(context.Background())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if v != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", v)
	}
}

func TestSuggestFromObserved(t *testing.T) {
	st, ctx := newStore(t)

	// Seed observed clients; the codex UA gets more hits and should win.
	codexUA := "codex_cli_rs/0.157.1 (Mac OS 15.0; arm64) tui"
	for i := 0; i < 5; i++ {
		if err := st.UpsertObservedClient(ctx, store.ObservedClient{
			UserAgent:  codexUA,
			Originator: "codex_cli_rs",
			Version:    "0.157.1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertObservedClient(ctx, store.ObservedClient{
		UserAgent:  "curl/8.4.0",
		Originator: "curl",
		Version:    "8.4.0",
	}); err != nil {
		t.Fatal(err)
	}

	ua, version, err := health.SuggestFromObserved(ctx, st, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if ua != codexUA {
		t.Fatalf("ua = %q, want %q", ua, codexUA)
	}
	if version != "0.157.1" {
		t.Fatalf("version = %q, want 0.157.1", version)
	}

	// Empty window -> no suggestion, no error.
	ua2, v2, err := health.SuggestFromObserved(ctx, st, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("suggest empty: %v", err)
	}
	if ua2 != "" || v2 != "" {
		t.Fatalf("expected empty suggestion, got (%q,%q)", ua2, v2)
	}
}
