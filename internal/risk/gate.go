package risk

import (
	"net/http"
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

// ClientGate checks the client type before any device lookup: non-Codex clients
// are rejected unless allowed, and — once require_codex_installation_id is on —
// "Codex" requests without an installation id are treated as impersonation.
func ClientGate(set settings.Settings, clientKind string, dev DeviceInfo) Decision {
	if clientKind != KindCodex && !set.AllowNonCodex {
		return Decision{Status: http.StatusForbidden, Code: "client_not_allowed",
			Message: "only the official Codex client is allowed with this key"}
	}
	if clientKind == KindCodex && set.RequireCodexInstallID && dev.InstallID == "" {
		return Decision{Status: http.StatusForbidden, Code: "client_not_allowed",
			Message: "Codex installation id missing; please update Codex to the latest version"}
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
			Message: "this device has been blocked for your key; contact the administrator"}
	case store.DeviceTrusted:
		return allow
	}
	if lax {
		return allow
	}
	unconfirmed := Decision{Status: http.StatusForbidden, Code: "device_unconfirmed",
		Message: "new device awaiting confirmation; confirm it in the portal: " + portal}
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
