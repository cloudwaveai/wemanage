package health

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"aiseat/internal/store"
)

// GitHub queries the GitHub REST API for release information. Base defaults to
// "https://api.github.com" when empty; HC defaults to http.DefaultClient.
type GitHub struct {
	Base string
	HC   *http.Client
}

// Latest returns the newest Codex release version, stripping a leading "rust-v"
// or "v" from the release tag (e.g. "rust-v0.157.1" -> "0.157.1").
func (g GitHub) Latest(ctx context.Context) (version string, err error) {
	base := g.Base
	if base == "" {
		base = "https://api.github.com"
	}
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}

	url := strings.TrimRight(base, "/") + "/repos/openai/codex/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return stripVersionPrefix(payload.TagName), nil
}

// stripVersionPrefix removes a leading "rust-v" or "v" from a release tag.
func stripVersionPrefix(tag string) string {
	tag = strings.TrimPrefix(tag, "rust-v")
	tag = strings.TrimPrefix(tag, "v")
	return tag
}

// SuggestFromObserved returns the most-frequently-seen Codex client UA and its
// parsed version among observed clients in the window [since, now]. The version
// is the token after the first '/' up to the first space in a "product/VERSION
// (...)" user-agent string.
func SuggestFromObserved(ctx context.Context, st *store.Store, since time.Time) (ua, version string, err error) {
	clients, err := st.TopObservedClients(ctx, since, 1)
	if err != nil {
		return "", "", err
	}
	if len(clients) == 0 {
		return "", "", nil
	}
	top := clients[0]
	return top.UserAgent, parseUAVersion(top.UserAgent), nil
}

// parseUAVersion extracts the version token from a "product/VERSION (...)" UA.
func parseUAVersion(ua string) string {
	slash := strings.IndexByte(ua, '/')
	if slash < 0 {
		return ""
	}
	rest := ua[slash+1:]
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		rest = rest[:sp]
	}
	return rest
}
