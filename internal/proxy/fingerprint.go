package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// UAParts is the decomposition of a Codex User-Agent string.
type UAParts struct {
	Product  string
	Version  string
	OS       string
	Terminal string
	Suffix   string
}

// codexUARe matches e.g.
// "codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.154.0)".
var codexUARe = regexp.MustCompile(`^(\S+)/(\S+) \(([^)]*)\) (\S+)(?: \((.*)\))?$`)

// ParseCodexUA parses a Codex User-Agent string into its parts. The trailing
// parenthesized suffix is optional. It returns ok=false when ua does not match.
func ParseCodexUA(ua string) (UAParts, bool) {
	m := codexUARe.FindStringSubmatch(ua)
	if m == nil {
		return UAParts{}, false
	}
	return UAParts{
		Product:  m[1],
		Version:  m[2],
		OS:       m[3],
		Terminal: m[4],
		Suffix:   m[5],
	}, true
}

// RenderUA fills the profile template placeholders {version}, {os} and
// {terminal} from the profile and device.
func RenderUA(p settings.CodexProfile, d store.DeviceProfile) string {
	return strings.NewReplacer(
		"{version}", p.Version,
		"{os}", d.OS,
		"{terminal}", d.Terminal,
	).Replace(p.UATemplate)
}

// codexDropHeaders is the set of canonical header names stripped from a Codex
// client's request before forwarding upstream (in addition to any "Cf-*").
var codexDropHeaders = func() map[string]bool {
	names := []string{
		"Authorization", "Cookie", "Host", "Connection", "Keep-Alive",
		"Proxy-Connection", "Proxy-Authorization", "Te", "Trailer",
		"Transfer-Encoding", "Upgrade", "Content-Length",
		"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host",
		"X-Forwarded-Port", "X-Real-Ip", "Forwarded", "Via",
		"True-Client-Ip", "X-Api-Key",
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[http.CanonicalHeaderKey(n)] = true
	}
	return m
}()

// HeaderInput carries everything needed to build the upstream header set.
type HeaderInput struct {
	Kind      string
	Client    http.Header
	Profile   settings.CodexProfile
	Device    store.DeviceProfile
	SessionID string
	CPAAPIKey string
}

// BuildUpstreamHeaders returns the header set to send to CPA.
//
// For KindCodex the client's headers are cloned, hop-by-hop and identifying
// headers are stripped, and Authorization/Content-Type/Accept are (re)set. For
// any other kind a fresh, minimal header set is built from the profile/device.
func BuildUpstreamHeaders(in HeaderInput) http.Header {
	if in.Kind == KindCodex {
		out := in.Client.Clone()
		if out == nil {
			out = http.Header{}
		}
		for k := range out {
			if codexDropHeaders[k] || strings.HasPrefix(k, "Cf-") {
				delete(out, k)
			}
		}
		out.Set("Authorization", "Bearer "+in.CPAAPIKey)
		out.Set("Content-Type", "application/json")
		setAccept(out, in.Client)
		if in.Client.Get("Session-Id") == "" && in.Client.Get("Session_id") == "" {
			out.Set("Session-Id", in.SessionID)
		}
		return out
	}

	// Non-Codex: emit only the whitelisted synthetic identity headers.
	out := http.Header{}
	out.Set("User-Agent", RenderUA(in.Profile, in.Device))
	out.Set("Originator", in.Profile.Originator)
	out.Set("Version", in.Profile.Version)
	out.Set("Session-Id", in.SessionID)
	out.Set("Authorization", "Bearer "+in.CPAAPIKey)
	out.Set("Content-Type", "application/json")
	setAccept(out, in.Client)
	return out
}

// setAccept preserves the client's Accept header if present, else defaults to
// text/event-stream.
func setAccept(out, client http.Header) {
	if acc := client.Get("Accept"); acc != "" {
		out.Set("Accept", acc)
	} else {
		out.Set("Accept", "text/event-stream")
	}
}

// SessionUUID derives a stable RFC-4122-formatted, v4-looking UUID string from
// any seed: sha256(seed), take the first 16 bytes, set the version nibble to 4
// and the variant bits, then format as 8-4-4-4-12 hex.
func SessionUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC-4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
