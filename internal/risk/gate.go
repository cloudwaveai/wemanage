package risk

import (
	"net"
	"net/http"
	"sync"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// Decision is the outcome of the synchronous data-plane gate.
type Decision struct {
	Allow   bool
	Status  int
	Code    string // OpenAI-style error code
	Message string
}

var allow = Decision{Allow: true}

// ClientGate checks the client before any device lookup: once
// require_codex_installation_id is on, "Codex" requests without an
// installation id are treated as impersonation. Whether clients other than
// Codex may be used is up to each account (see the proxy's account picking).
func ClientGate(set settings.Settings, clientKind string, dev DeviceInfo) Decision {
	if clientKind == KindCodex && set.RequireCodexInstallID && dev.InstallID == "" {
		return Decision{Status: http.StatusForbidden, Code: "client_not_allowed",
			Message: "Codex 版本太旧，请升级到最新版后再用。"}
	}
	return allow
}

// DeviceGate decides whether a request from the device may proceed. created
// reports that the device was registered by this very request.
func DeviceGate(set settings.Settings, dev CachedDevice, created bool, now time.Time, portal string) Decision {
	lax := set.RiskMode == settings.RiskLax
	switch dev.Status {
	case store.DeviceBlocked:
		return Decision{Status: http.StatusForbidden, Code: "device_blocked",
			Message: "这台电脑已被停用（在门户点过「不是我」，或被管理员停用）。如需恢复，请联系管理员。"}
	case store.DeviceTrusted:
		return allow
	}
	if lax {
		return allow
	}
	unconfirmed := Decision{Status: http.StatusForbidden, Code: "device_unconfirmed",
		Message: "这台新电脑还没确认是你本人。打开 " + portal + " 点「是我」，确认后就能继续用。"}
	if set.RiskMode == settings.RiskStrict {
		return unconfirmed
	}
	// standard: a pending device works during the confirmation window.
	if dev.Status == store.DeviceExpired {
		return unconfirmed
	}
	if !created && now.Sub(dev.FirstSeen) > time.Duration(set.DeviceConfirmHours)*time.Hour {
		return unconfirmed
	}
	return allow
}

// KindCodex mirrors proxy.KindCodex (kept here to avoid an import cycle).
const KindCodex = "codex"

// OfficeMatcher caches the parsed office networks between requests.
type OfficeMatcher struct {
	mu   sync.Mutex
	list []string
	nets []*net.IPNet
}

// Contains reports whether ip is inside one of the listed networks.
func (m *OfficeMatcher) Contains(list []string, ip string) bool {
	m.mu.Lock()
	if !sameStrings(m.list, list) {
		nets, err := settings.ParseNetworks(list)
		if err != nil {
			nets = nil
		}
		m.list, m.nets = append([]string(nil), list...), nets
	}
	nets := m.nets
	m.mu.Unlock()
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NetworkGate enforces "office networks only" when it is switched on.
// Employees marked remote_allowed are exempt (they remain subject to the
// device and concurrency rules).
func NetworkGate(set settings.Settings, remoteAllowed bool, ip string, m *OfficeMatcher) Decision {
	if !set.OfficeOnly || remoteAllowed || m.Contains(set.OfficeNetworks, ip) {
		return allow
	}
	return Decision{Status: http.StatusForbidden, Code: "network_not_allowed",
		Message: "这个 Key 只能在公司网络里使用。需要在外面用，请联系管理员开通远程使用。"}
}
