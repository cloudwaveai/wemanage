package proxy

import (
	"net/http"
	"regexp"
	"sort"
	"testing"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

func testProfile() settings.CodexProfile {
	return settings.CodexProfile{
		Version:    "0.157.1",
		UATemplate: "codex-tui/{version} ({os}) {terminal} (codex-tui; {version})",
		Originator: "codex-tui",
	}
}

func testDevice() store.DeviceProfile {
	return store.DeviceProfile{
		OS:       "Mac OS 26.5.2; arm64",
		Terminal: "iTerm.app/3.6.11",
	}
}

func TestBuildUpstreamHeadersCodex(t *testing.T) {
	client := http.Header{}
	client.Set("X-Forwarded-For", "203.0.113.7")
	client.Set("Cookie", "sid=secret")
	client.Set("X-Codex-Turn-Metadata", "turn-meta")
	client.Set("Version", "0.157.1")
	client.Set("User-Agent", "codex-tui/0.157.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.157.1)")
	client.Set("Cf-Connecting-Ip", "203.0.113.7")
	client.Set("Authorization", "Bearer client-token")

	out := BuildUpstreamHeaders(HeaderInput{
		Kind:      KindCodex,
		Client:    client,
		SessionID: "sess-codex",
		CPAAPIKey: "cpakey",
	})

	if got := out.Get("X-Codex-Turn-Metadata"); got != "turn-meta" {
		t.Errorf("X-Codex-Turn-Metadata: got %q, want %q", got, "turn-meta")
	}
	if got := out.Get("Version"); got != "0.157.1" {
		t.Errorf("Version: got %q, want %q", got, "0.157.1")
	}
	if got := out.Get("X-Forwarded-For"); got != "" {
		t.Errorf("X-Forwarded-For should be dropped, got %q", got)
	}
	if got := out.Get("Cookie"); got != "" {
		t.Errorf("Cookie should be dropped, got %q", got)
	}
	if got := out.Get("Cf-Connecting-Ip"); got != "" {
		t.Errorf("Cf-Connecting-Ip should be dropped, got %q", got)
	}
	if got := out.Get("Authorization"); got != "Bearer cpakey" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer cpakey")
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/json")
	}
	// Client sent no Accept -> default.
	if got := out.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept: got %q, want %q", got, "text/event-stream")
	}
	// Neither Session-Id nor Session_id present on client -> set.
	if got := out.Get("Session-Id"); got != "sess-codex" {
		t.Errorf("Session-Id: got %q, want %q", got, "sess-codex")
	}
}

func TestBuildUpstreamHeadersCodexPreservesAcceptAndSession(t *testing.T) {
	client := http.Header{}
	client.Set("Accept", "application/json")
	client.Set("Session_id", "client-sess")

	out := BuildUpstreamHeaders(HeaderInput{
		Kind:      KindCodex,
		Client:    client,
		SessionID: "gateway-sess",
		CPAAPIKey: "cpakey",
	})
	if got := out.Get("Accept"); got != "application/json" {
		t.Errorf("Accept should be preserved: got %q", got)
	}
	// Client already supplied Session_id -> gateway must not add Session-Id.
	if got := out.Get("Session-Id"); got != "" {
		t.Errorf("Session-Id should not be set when client has Session_id, got %q", got)
	}
	if got := out.Get("Session_id"); got != "client-sess" {
		t.Errorf("Session_id should be preserved: got %q", got)
	}
}

func TestBuildUpstreamHeadersOther(t *testing.T) {
	client := http.Header{}
	client.Set("X-Stainless-Lang", "js")
	client.Set("User-Agent", "OpenAI/JS 4.0")
	client.Set("Http-Referer", "https://cherry.example")
	client.Set("X-Title", "Cherry Studio")

	profile := testProfile()
	device := testDevice()

	out := BuildUpstreamHeaders(HeaderInput{
		Kind:      KindOther,
		Client:    client,
		Profile:   profile,
		Device:    device,
		SessionID: "sess-other",
		CPAAPIKey: "cpakey",
	})

	wantKeys := []string{
		"Accept", "Authorization", "Content-Type",
		"Originator", "Session-Id", "User-Agent", "Version",
	}
	gotKeys := make([]string, 0, len(out))
	for k := range out {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("key set: got %v, want %v", gotKeys, wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("key set: got %v, want %v", gotKeys, wantKeys)
		}
	}

	if got, want := out.Get("User-Agent"), RenderUA(profile, device); got != want {
		t.Errorf("User-Agent: got %q, want %q", got, want)
	}
	if got := out.Get("X-Stainless-Lang"); got != "" {
		t.Errorf("X-Stainless-Lang must not pass through, got %q", got)
	}
	if got := out.Get("Http-Referer"); got != "" {
		t.Errorf("Http-Referer must not pass through, got %q", got)
	}
	if got := out.Get("Originator"); got != profile.Originator {
		t.Errorf("Originator: got %q, want %q", got, profile.Originator)
	}
	if got := out.Get("Version"); got != profile.Version {
		t.Errorf("Version: got %q, want %q", got, profile.Version)
	}
	if got := out.Get("Session-Id"); got != "sess-other" {
		t.Errorf("Session-Id: got %q, want %q", got, "sess-other")
	}
	if got := out.Get("Authorization"); got != "Bearer cpakey" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer cpakey")
	}
	if got := out.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept: got %q, want %q", got, "text/event-stream")
	}
}

func TestParseCodexUA(t *testing.T) {
	ua := "codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.154.0)"
	got, ok := ParseCodexUA(ua)
	if !ok {
		t.Fatalf("ParseCodexUA(%q) ok=false, want true", ua)
	}
	want := UAParts{
		Product:  "codex-tui",
		Version:  "0.154.0",
		OS:       "Mac OS 26.5.2; arm64",
		Terminal: "iTerm.app/3.6.11",
		Suffix:   "codex-tui; 0.154.0",
	}
	if got != want {
		t.Errorf("ParseCodexUA: got %+v, want %+v", got, want)
	}

	if _, ok := ParseCodexUA("curl/8.1"); ok {
		t.Error("ParseCodexUA(\"curl/8.1\") ok=true, want false")
	}
}

func TestRenderUA(t *testing.T) {
	p := settings.CodexProfile{
		UATemplate: "codex-tui/{version} ({os}) {terminal} (codex-tui; {version})",
		Version:    "0.157.1",
	}
	d := store.DeviceProfile{
		OS:       "Mac OS 26.5.2; arm64",
		Terminal: "iTerm.app/3.6.11",
	}
	want := "codex-tui/0.157.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.157.1)"
	if got := RenderUA(p, d); got != want {
		t.Errorf("RenderUA: got %q, want %q", got, want)
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestSessionUUID(t *testing.T) {
	a := SessionUUID("seed-one")
	b := SessionUUID("seed-one")
	c := SessionUUID("seed-two")

	if a != b {
		t.Errorf("SessionUUID not stable: %q != %q", a, b)
	}
	if a == c {
		t.Errorf("SessionUUID for different seeds should differ, both %q", a)
	}
	for _, u := range []string{a, c, SessionUUID("")} {
		if !uuidRe.MatchString(u) {
			t.Errorf("SessionUUID %q does not match v4 format", u)
		}
	}
}
