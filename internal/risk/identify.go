// Package risk detects key sharing and resale: it registers the devices and
// networks each employee's key is used from, gates requests from blocked or
// unconfirmed devices and non-Codex clients, and raises risk events (and
// freezes) for patterns such as concurrent use from different networks.
package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

// ClientIP returns the caller's IP. When trustProxy is set and the direct peer
// is on a private/loopback network (i.e. our own Caddy), the rightmost
// X-Forwarded-For entry is used — the one appended by that trusted proxy.
// Client-supplied X-Forwarded-For from the public internet is never trusted.
func ClientIP(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if trustProxy && peer != nil && (peer.IsPrivate() || peer.IsLoopback() || peer.IsLinkLocalUnicast()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				if ip := net.ParseIP(strings.TrimSpace(parts[i])); ip != nil {
					return ip.String()
				}
			}
		}
		if xr := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-Ip"))); xr != nil {
			return xr.String()
		}
	}
	if peer != nil {
		return peer.String()
	}
	return host
}

// DeviceInfo identifies the client device behind a request.
type DeviceInfo struct {
	Key       string // "inst:<installation id>" or "ua:<hash>"
	Kind      string // codex_install | ua
	Label     string // human readable
	InstallID string
	UserAgent string
}

var codexUA = regexp.MustCompile(`^(\S+)/(\S+) \(([^)]*)\) (\S+)`)

// ExtractDevice derives a stable device identity. A Codex installation id (sent
// in the request body's client_metadata) is the strongest signal; without it we
// fall back to a hash of the User-Agent and Originator.
func ExtractDevice(h http.Header, body []byte) DeviceInfo {
	ua := h.Get("User-Agent")
	info := DeviceInfo{UserAgent: ua, Label: deviceLabel(ua)}
	inst := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String())
	if inst == "" {
		inst = strings.TrimSpace(h.Get("X-Codex-Installation-Id"))
	}
	if inst != "" {
		info.InstallID = inst
		info.Kind = "codex_install"
		info.Key = "inst:" + inst
		return info
	}
	sum := sha256.Sum256([]byte(ua + "|" + h.Get("Originator")))
	info.Kind = "ua"
	info.Key = "ua:" + hex.EncodeToString(sum[:8])
	return info
}

func deviceLabel(ua string) string {
	if m := codexUA.FindStringSubmatch(ua); m != nil {
		return m[3] + " · " + m[4] + " · " + m[1] + " " + m[2]
	}
	if ua == "" {
		return "未知客户端"
	}
	if len(ua) > 80 {
		return ua[:80] + "…"
	}
	return ua
}

// NetKeys returns the network key used for registry (IPv4 /24, IPv6 /48) and a
// coarser key used to decide whether two uses are on "different networks"
// (IPv4 /16, IPv6 /32). Office NAT pools and mobile carriers usually stay
// within one /16, which keeps false positives down.
func NetKeys(ip string) (key, coarse string) {
	p := net.ParseIP(ip)
	if p == nil {
		return "", ""
	}
	if v4 := p.To4(); v4 != nil {
		return net.IPv4(v4[0], v4[1], v4[2], 0).String() + "/24",
			net.IPv4(v4[0], v4[1], 0, 0).String() + "/16"
	}
	m48 := net.CIDRMask(48, 128)
	m32 := net.CIDRMask(32, 128)
	return p.Mask(m48).String() + "/48", p.Mask(m32).String() + "/32"
}
