package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Content-Length":      true,
}

// copyResponseHeaders copies upstream headers to the client, dropping hop-by-hop.
func copyResponseHeaders(w http.ResponseWriter, src http.Header) {
	dst := w.Header()
	for k, vs := range src {
		if hopByHop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isEventStream(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Content-Type")), "text/event-stream")
}

// flushCopy streams src to w, flushing after each chunk so SSE reaches the client live.
func flushCopy(w http.ResponseWriter, src io.Reader) {
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// parseRateLimitReset derives a rate-limit deadline from upstream headers,
// five minutes from now when they give none.
func parseRateLimitReset(h http.Header) time.Time {
	now := time.Now()
	if t, ok := headerReset(h, now); ok {
		return t
	}
	return now.Add(5 * time.Minute)
}

// headerReset reads a rate-limit deadline from upstream headers.
func headerReset(h http.Header, now time.Time) (time.Time, bool) {
	if v := h.Get("X-Codex-Primary-Reset-At"); v != "" {
		if secs, err := strconv.ParseInt(v, 10, 64); err == nil && secs > 0 {
			return time.Unix(secs, 0), true
		}
	}
	if v := h.Get("X-Codex-Primary-Reset-After-Seconds"); v != "" {
		if s, err := strconv.Atoi(v); err == nil && s > 0 {
			return now.Add(time.Duration(s) * time.Second), true
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(v); err == nil && s > 0 {
			return now.Add(time.Duration(s) * time.Second), true
		}
	}
	return time.Time{}, false
}

func hashSeed(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
