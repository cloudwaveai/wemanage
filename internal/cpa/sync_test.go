package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiseat/internal/store"
)

func TestReconcileAccountFilePresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		io.WriteString(w, `{"files":[{"name":"codex-1.json","type":"codex","email":"a@b.c","provider":"openai","status":"active"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	acct := store.Account{ID: 1, CPAAuthName: "codex-1.json"}
	drift, err := ReconcileAccount(context.Background(), c, acct, acct.Prefix(), "http://proxy.example")
	if err != nil {
		t.Fatalf("ReconcileAccount: %v", err)
	}
	if drift != "" {
		t.Errorf("drift = %q, want empty (file present)", drift)
	}
}

func TestReconcileAccountFileAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		io.WriteString(w, `{"files":[]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	acct := store.Account{ID: 1, CPAAuthName: "codex-1.json"}
	drift, err := ReconcileAccount(context.Background(), c, acct, acct.Prefix(), "http://proxy.example")
	if err != nil {
		t.Fatalf("ReconcileAccount: %v", err)
	}
	if !bytes.Contains([]byte(drift), []byte("missing")) {
		t.Errorf("drift = %q, want it to mention %q", drift, "missing")
	}
}

func TestApplyExpected(t *testing.T) {
	const proxy = "http://user:pass@10.0.0.1:8080"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodPatch || r.URL.Path != "/v0/management/auth-files/fields" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("decode request body %q: %v", string(body), err)
			return
		}
		if m["name"] != "codex-1.json" {
			t.Errorf("name = %v, want codex-1.json", m["name"])
		}
		if m["prefix"] != "acct-1" {
			t.Errorf("prefix = %v, want acct-1", m["prefix"])
		}
		if m["proxy_url"] != proxy {
			t.Errorf("proxy_url = %v, want %q", m["proxy_url"], proxy)
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	acct := store.Account{ID: 1, CPAAuthName: "codex-1.json"}
	if err := ApplyExpected(context.Background(), c, acct, acct.Prefix(), proxy); err != nil {
		t.Fatalf("ApplyExpected: %v", err)
	}
}
