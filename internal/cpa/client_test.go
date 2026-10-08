package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testKey is the management bearer key used across all fake servers.
const testKey = "mgmt-secret-key"

// assertAuth verifies a management request carries the bearer header. It is
// safe to call from a server handler goroutine because testing.T is safe for
// concurrent use (it uses t.Errorf, never t.Fatal, from other goroutines).
func assertAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got, want := r.Header.Get("Authorization"), "Bearer "+testKey; got != want {
		t.Errorf("%s %s: Authorization = %q, want %q", r.Method, r.URL.Path, got, want)
	}
}

func TestListAuthFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodGet || r.URL.Path != "/v0/management/auth-files" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"observed_at":"2026-09-27T00:00:00Z","files":[{"name":"codex-1.json","type":"codex","email":"a@b.c","provider":"openai","status":"active"}]}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	files, err := c.ListAuthFiles(context.Background())
	if err != nil {
		t.Fatalf("ListAuthFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	got := files[0]
	want := AuthFile{Name: "codex-1.json", Type: "codex", Email: "a@b.c", Provider: "openai", Status: "active"}
	if got != want {
		t.Errorf("file = %+v, want %+v", got, want)
	}
}

func TestRequestCodexAuthURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodGet || r.URL.Path != "/v0/management/codex-auth-url" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","url":"https://x","state":"s1"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	u, state, err := c.RequestCodexAuthURL(context.Background())
	if err != nil {
		t.Fatalf("RequestCodexAuthURL: %v", err)
	}
	if u != "https://x" {
		t.Errorf("url = %q, want %q", u, "https://x")
	}
	if state != "s1" {
		t.Errorf("state = %q, want %q", state, "s1")
	}
}

func TestPostOAuthCallback(t *testing.T) {
	const redirect = "http://localhost:1455/cb?code=abc&state=s1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/oauth-callback" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("decode request body %q: %v", string(body), err)
			return
		}
		if m["provider"] != "codex" {
			t.Errorf("provider = %v, want codex", m["provider"])
		}
		if m["redirect_url"] != redirect {
			t.Errorf("redirect_url = %v, want %q", m["redirect_url"], redirect)
		}
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	if err := c.PostOAuthCallback(context.Background(), "codex", redirect); err != nil {
		t.Fatalf("PostOAuthCallback: %v", err)
	}
}

func TestAuthStatus(t *testing.T) {
	// Each case is served by its own fixed-response server so there is no shared
	// mutable state across goroutines (keeps -race clean).
	cases := []struct {
		name string
		body string
		want string
	}{
		{"ok", `{"status":"ok"}`, "ok"},
		{"error", `{"status":"error","error":"nope"}`, "error"},
		{"pending", `{"status":"pending"}`, "pending"},
		{"unknown-maps-to-pending", `{"status":"whatever"}`, "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertAuth(t, r)
				if r.Method != http.MethodGet || r.URL.Path != "/v0/management/get-auth-status" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("state") != "s1" {
					t.Errorf("state query = %q, want s1", r.URL.Query().Get("state"))
				}
				io.WriteString(w, body)
			}))
			defer srv.Close()

			c := NewClient(srv.URL, testKey, srv.Client())
			got, err := c.AuthStatus(context.Background(), "s1")
			if err != nil {
				t.Fatalf("AuthStatus: %v", err)
			}
			if got != tc.want {
				t.Errorf("AuthStatus(%s) = %q, want %q", body, got, tc.want)
			}
		})
	}
}

func TestNon2xxErrorIncludesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	_, err := c.ListAuthFiles(context.Background())
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	msg := []byte(err.Error())
	if !bytes.Contains(msg, []byte("401")) {
		t.Errorf("error %q should mention status 401", err)
	}
	if !bytes.Contains(msg, []byte("bad key")) {
		t.Errorf("error %q should include response body text", err)
	}
}

func TestUploadAuthFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/auth-files" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if got := r.URL.Query().Get("name"); got != "codex-2.json" {
			t.Errorf("name = %q, want codex-2.json (CPA reads it from the query)", got)
		}
		if string(body) != `{"k":"v"}` {
			t.Errorf("body = %q, want the file itself", body)
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	if err := c.UploadAuthFile(context.Background(), "codex-2.json", []byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("UploadAuthFile: %v", err)
	}
}

func TestDeleteAuthFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		if r.Method != http.MethodDelete || r.URL.Path != "/v0/management/auth-files" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("name") != "codex-1.json" {
			t.Errorf("name query = %q, want codex-1.json", r.URL.Query().Get("name"))
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	if err := c.DeleteAuthFile(context.Background(), "codex-1.json"); err != nil {
		t.Fatalf("DeleteAuthFile: %v", err)
	}
}

// TestAuthHeaderOnEveryRequest exercises every client method against one server
// and asserts (via assertAuth inside the handler) that each request carries the
// management bearer header.
func TestAuthHeaderOnEveryRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		switch r.URL.Path {
		case "/v0/management/auth-files":
			io.WriteString(w, `{"files":[]}`)
		case "/v0/management/codex-auth-url":
			io.WriteString(w, `{"status":"ok","url":"https://x","state":"s"}`)
		default:
			io.WriteString(w, `{"status":"ok"}`)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testKey, srv.Client())
	ctx := context.Background()
	_, _ = c.ListAuthFiles(ctx)
	_ = c.UploadAuthFile(ctx, "codex-1.json", []byte("{}"))
	_ = c.DeleteAuthFile(ctx, "codex-1.json")
	_ = c.PatchFields(ctx, "codex-1.json", map[string]any{"prefix": "acct-1"})
	_, _, _ = c.RequestCodexAuthURL(ctx)
	_ = c.PostOAuthCallback(ctx, "codex", "http://x")
	_, _ = c.AuthStatus(ctx, "s")
}
