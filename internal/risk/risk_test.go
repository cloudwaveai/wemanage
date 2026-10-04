package risk

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

func TestClientIPTrustsXFFOnlyFromPrivatePeer(t *testing.T) {
	cases := []struct {
		name, remote, xff string
		trust             bool
		want              string
	}{
		{"private peer, rightmost xff", "172.18.0.5:4000", "6.6.6.6, 1.2.3.4", true, "1.2.3.4"},
		{"loopback peer", "127.0.0.1:4000", "8.8.8.8", true, "8.8.8.8"},
		{"public peer ignores spoofed xff", "203.0.113.9:4000", "10.0.0.1", true, "203.0.113.9"},
		{"trust disabled", "172.18.0.5:4000", "1.2.3.4", false, "172.18.0.5"},
		{"garbage xff falls back to peer", "10.0.0.2:1", "not-an-ip", true, "10.0.0.2"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, c.trust); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestExtractDevicePrefersInstallationID(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "codex-tui/0.157.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.157.1)")
	h.Set("Originator", "codex-tui")
	body := []byte(`{"model":"x","client_metadata":{"x-codex-installation-id":"abc-123"}}`)
	d := ExtractDevice(h, body)
	if d.Key != "inst:abc-123" || d.Kind != "codex_install" {
		t.Fatalf("got %+v", d)
	}
	if d.Label != "Mac OS 26.5.2; arm64 · iTerm.app/3.6.11 · codex-tui 0.157.1" {
		t.Fatalf("label = %q", d.Label)
	}

	// Without an installation id: stable UA hash.
	d1 := ExtractDevice(h, []byte(`{"model":"x"}`))
	d2 := ExtractDevice(h, []byte(`{"model":"y"}`))
	if d1.Kind != "ua" || d1.Key != d2.Key || len(d1.Key) != len("ua:")+16 {
		t.Fatalf("ua fallback unstable: %+v %+v", d1, d2)
	}
	h.Set("User-Agent", "codex-tui/0.158.0 (Linux; x64) xterm (codex-tui; 0.158.0)")
	if d3 := ExtractDevice(h, nil); d3.Key == d1.Key {
		t.Fatal("different UA should give a different device key")
	}
}

func TestNetKeys(t *testing.T) {
	k, c := NetKeys("1.2.3.4")
	if k != "1.2.3.0/24" || c != "1.2.0.0/16" {
		t.Fatalf("v4: %s %s", k, c)
	}
	k, c = NetKeys("2001:db8:abcd:12::1")
	if k != "2001:db8:abcd::/48" || c != "2001:db8::/32" {
		t.Fatalf("v6: %s %s", k, c)
	}
	if k, _ := NetKeys("nope"); k != "" {
		t.Fatal("invalid ip should give empty key")
	}
}

func TestParseRegion(t *testing.T) {
	g := ParseRegion("中国|0|广东省|深圳市|电信")
	if g.Country != "中国" || g.Province != "广东省" || g.City != "深圳市" || g.ISP != "电信" || g.Datacenter {
		t.Fatalf("5-field: %+v", g)
	}
	g = ParseRegion("中国|浙江省|杭州市|阿里云")
	if g.Province != "浙江省" || !g.Datacenter {
		t.Fatalf("4-field: %+v", g)
	}
	if !IsDatacenterISP("Amazon.com, Inc.") || IsDatacenterISP("联通") {
		t.Fatal("datacenter ISP detection")
	}
}

func TestClientGate(t *testing.T) {
	set := settings.Defaults()
	// Whether other clients may be used is up to each account (the proxy's
	// account picking), not this gate.
	if d := ClientGate(set, "other", DeviceInfo{}); !d.Allow {
		t.Fatalf("the gate leaves other clients to the account policy: %+v", d)
	}
	if d := ClientGate(set, KindCodex, DeviceInfo{}); !d.Allow {
		t.Fatal("codex allowed")
	}
	set.RequireCodexInstallID = true
	if d := ClientGate(set, KindCodex, DeviceInfo{}); d.Allow {
		t.Fatal("codex without installation id should be blocked when required")
	}
	if d := ClientGate(set, KindCodex, DeviceInfo{InstallID: "x"}); !d.Allow {
		t.Fatal("codex with installation id allowed")
	}
}

func TestDeviceGate(t *testing.T) {
	now := time.Now()
	std := settings.Defaults()
	strict := settings.Defaults()
	strict.RiskMode = settings.RiskStrict
	lax := settings.Defaults()
	lax.RiskMode = settings.RiskLax

	fresh := CachedDevice{ID: 1, Status: store.DevicePending, FirstSeen: now}
	old := CachedDevice{ID: 1, Status: store.DevicePending, FirstSeen: now.Add(-25 * time.Hour)}
	expired := CachedDevice{ID: 1, Status: store.DeviceExpired, FirstSeen: now.Add(-25 * time.Hour)}
	blocked := CachedDevice{ID: 1, Status: store.DeviceBlocked}
	trusted := CachedDevice{ID: 1, Status: store.DeviceTrusted}

	cases := []struct {
		name    string
		set     settings.Settings
		dev     CachedDevice
		created bool
		allow   bool
		code    string
	}{
		{"standard new device allowed", std, fresh, true, true, ""},
		{"standard pending within window", std, fresh, false, true, ""},
		{"standard pending past window", std, old, false, false, "device_unconfirmed"},
		{"standard expired", std, expired, false, false, "device_unconfirmed"},
		{"strict new device blocked", strict, fresh, true, false, "device_unconfirmed"},
		{"lax expired allowed", lax, expired, false, true, ""},
		{"blocked always", lax, blocked, false, false, "device_blocked"},
		{"trusted", strict, trusted, false, true, ""},
	}
	for _, c := range cases {
		d := DeviceGate(c.set, c.dev, c.created, now, "http://portal/devices")
		if d.Allow != c.allow || d.Code != c.code {
			t.Errorf("%s: got %+v", c.name, d)
		}
	}
}

func TestEvalWindow(t *testing.T) {
	now := time.Now()
	grace := 30 * time.Minute
	old := now.Add(-2 * time.Hour)

	// Trusted devices on two networks: not flagged.
	ss := []sighting{
		{Device: 1, Net: "1.2.0.0/16", At: now, Trusted: true, FirstSeen: old},
		{Device: 2, Net: "5.6.0.0/16", At: now, Trusted: true, FirstSeen: old},
	}
	if f := evalWindow(ss, now, grace, 3); len(f) != 0 {
		t.Fatalf("trusted pair flagged: %+v", f)
	}

	// Untrusted device inside the grace period: not flagged.
	ss[1].Trusted, ss[1].FirstSeen = false, now.Add(-5*time.Minute)
	if f := evalWindow(ss, now, grace, 3); len(f) != 0 {
		t.Fatalf("grace period ignored: %+v", f)
	}

	// Untrusted past grace on a different network: concurrent_untrusted.
	ss[1].FirstSeen = old
	f := evalWindow(ss, now, grace, 3)
	if len(f) != 1 || f[0].Rule != RuleConcurrentUntrusted || f[0].DeviceID != 2 {
		t.Fatalf("want concurrent_untrusted on device 2, got %+v", f)
	}

	// Same network (office NAT): not flagged.
	ss[1].Net = ss[0].Net
	if f := evalWindow(ss, now, grace, 3); len(f) != 0 {
		t.Fatalf("same network flagged: %+v", f)
	}

	// Three networks, all trusted: multi_network.
	ss = []sighting{
		{Device: 1, Net: "a", Trusted: true, FirstSeen: old},
		{Device: 2, Net: "b", Trusted: true, FirstSeen: old},
		{Device: 3, Net: "c", Trusted: true, FirstSeen: old},
	}
	f = evalWindow(ss, now, grace, 3)
	if len(f) != 1 || f[0].Rule != RuleMultiNetwork {
		t.Fatalf("want multi_network, got %+v", f)
	}
}

func TestWindowPrunesAndMerges(t *testing.T) {
	w := newWindow()
	t0 := time.Now()
	span := 10 * time.Minute
	w.add(1, sighting{Device: 1, Net: "a", At: t0}, span)
	w.add(1, sighting{Device: 1, Net: "a", At: t0.Add(time.Minute)}, span)
	snap := w.add(1, sighting{Device: 2, Net: "b", At: t0.Add(2 * time.Minute)}, span)
	if len(snap) != 2 {
		t.Fatalf("merge failed: %+v", snap)
	}
	snap = w.add(1, sighting{Device: 3, Net: "c", At: t0.Add(20 * time.Minute)}, span)
	if len(snap) != 1 || snap[0].Device != 3 {
		t.Fatalf("prune failed: %+v", snap)
	}
	w.setTrusted(1, 3, true)
	w.forget(1)
	if snap := w.add(1, sighting{Device: 4, Net: "d", At: t0}, span); len(snap) != 1 {
		t.Fatalf("forget failed: %+v", snap)
	}
}
