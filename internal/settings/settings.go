// Package settings holds tunable, DB-backed configuration with defaults and a read cache.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"aiseat/internal/store"
)

const settingsKey = "gateway"

// CodexProfile is the template used to render a synthetic Codex client identity.
type CodexProfile struct {
	Version    string `json:"version"`
	UATemplate string `json:"ua_template"`
	Originator string `json:"originator"`
}

// ClientRule classifies a client by matching a header against a regex.
type ClientRule struct {
	Kind    string `json:"kind"`
	Header  string `json:"header"`
	Pattern string `json:"pattern"`
}

// RedactionRule is a named regex applied to logged bodies.
type RedactionRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Settings is the full tunable configuration.
type Settings struct {
	DefaultMaxConcurrency  int                 `json:"default_max_concurrency"`
	QueueTimeoutSeconds    int                 `json:"queue_timeout_seconds"`
	RetentionDays          int                 `json:"retention_days"`
	CodexProfile           CodexProfile        `json:"codex_profile"`
	ClientRules            []ClientRule        `json:"client_rules"`
	RedactionRules         []RedactionRule     `json:"redaction_rules"`
	ModelMismatchThreshold int                 `json:"model_mismatch_threshold"`
	ReportEnabled          bool                `json:"report_enabled"`
	ReportLLMModel         string              `json:"report_llm_model"`
	DefaultDeviceProfile   store.DeviceProfile `json:"default_device_profile"`

	// Risk controls (anti key-sharing).
	RiskMode                 string `json:"risk_mode"`       // lax | standard | strict
	AllowNonCodex            bool   `json:"allow_non_codex"` // false = block non-Codex clients
	DeviceConfirmHours       int    `json:"device_confirm_hours"`
	ConcurrencyWindowMinutes int    `json:"concurrency_window_minutes"`
	NewDeviceGraceMinutes    int    `json:"new_device_grace_minutes"`
	MaxActiveDevices         int    `json:"max_active_devices"`
	RequireCodexInstallID    bool   `json:"require_codex_installation_id"`

	// Office networks: when OfficeOnly is on, keys work only from these
	// networks (CIDRs or single IPs), except for employees marked remote_allowed.
	OfficeOnly     bool     `json:"office_only"`
	OfficeNetworks []string `json:"office_networks"`

	// DisableWebsocket turns off the Responses WebSocket transport: handshakes
	// get 426 and Codex falls back to HTTP (kill switch).
	DisableWebsocket bool `json:"disable_websocket"`

	// DefaultCodexModel is the model one-click setup recommends for ChatGPT
	// accounts. When an account lacks it, the newest model of the same family
	// (the last "-" part, e.g. "luna") is recommended instead.
	DefaultCodexModel string `json:"default_codex_model"`

	// MaxAccountMembers is how many employees may share one account.
	MaxAccountMembers int `json:"max_account_members"`

	// Content audit of the newest user message, before it is sent upstream.
	ContentAuditMode  string        `json:"content_audit_mode"` // off | warn | block
	ContentAuditRules []ContentRule `json:"content_audit_rules"`
}

// ContentRule is a named case-insensitive regex matched against the newest
// user message of a request.
type ContentRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Content audit modes.
const (
	AuditOff   = "off"
	AuditWarn  = "warn"
	AuditBlock = "block"
)

// DefaultContentRules target the cyber-abuse OpenAI bans accounts for:
// malware, credential theft, AV evasion, botnets/DDoS, phishing kits and
// weaponised exploits. They favour precision; admins can edit them.
func DefaultContentRules() []ContentRule {
	return []ContentRule{
		{Name: "ransomware", Pattern: `ransomware|勒索(软件|病毒)`},
		{Name: "credential-theft", Pattern: `keylogger|键盘记录器|(credential|password|cookie|token)[ _-]?stealer|窃取.{0,6}(密码|凭证|cookie|token)`},
		{Name: "av-evasion", Pattern: `免杀|bypass.{0,12}(antivirus|anti-virus|defender|edr)|绕过.{0,6}(杀软|杀毒|defender|edr)`},
		{Name: "botnet-ddos", Pattern: `botnet|僵尸网络|ddos.{0,12}(attack|tool|script|工具|脚本)|(发起|实施).{0,6}ddos`},
		{Name: "malware-build", Pattern: `(write|create|build|develop|编写|写一个|开发).{0,24}(malware|trojan|rootkit|backdoor|木马|后门|病毒)`},
		{Name: "phishing-kit", Pattern: `phishing.{0,8}(kit|page|site)|钓鱼(网站|页面|邮件模板)`},
		{Name: "weaponised-exploit", Pattern: `(weaponi[sz]e|working|reliable).{0,12}exploit|exploit.{0,20}(unauthori[sz]ed|without permission)|未授权.{0,6}(渗透|入侵)`},
	}
}

// Risk modes.
const (
	RiskLax      = "lax"
	RiskStandard = "standard"
	RiskStrict   = "strict"
)

// DefaultRedactionRules returns the built-in secret patterns.
func DefaultRedactionRules() []RedactionRule {
	return []RedactionRule{
		{Name: "openai-key", Pattern: `sk-[A-Za-z0-9_-]{20,}`},
		{Name: "aws-akia", Pattern: `AKIA[0-9A-Z]{16}`},
		{Name: "github-token", Pattern: `(ghp|github_pat)_[A-Za-z0-9_]{20,}`},
		{Name: "slack-token", Pattern: `xox[abpr]-[A-Za-z0-9-]{10,}`},
		{Name: "private-key", Pattern: `-----BEGIN [A-Z ]*PRIVATE KEY-----`},
		{Name: "jwt", Pattern: `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`},
		{Name: "url-cred", Pattern: `[a-z][a-z0-9+.-]*://[^\s:@/]+:[^\s:@/]+@`},
	}
}

// Defaults returns the baseline settings.
func Defaults() Settings {
	return Settings{
		DefaultMaxConcurrency: 4,
		QueueTimeoutSeconds:   60,
		RetentionDays:         180,
		CodexProfile: CodexProfile{
			Version:    "0.157.1",
			UATemplate: "codex-tui/{version} ({os}) {terminal} (codex-tui; {version})",
			Originator: "codex-tui",
		},
		// Case-insensitive: the Codex desktop app says "Codex Desktop/…".
		ClientRules: []ClientRule{
			{Kind: "codex", Header: "User-Agent", Pattern: `(?i)^(codex[-_ ]|codex_cli_rs|codex-tui|codex_vscode|codex_exec)`},
			{Kind: "codex", Header: "Originator", Pattern: `(?i)^codex`},
		},
		RedactionRules:         DefaultRedactionRules(),
		ModelMismatchThreshold: 3,
		ReportEnabled:          true,
		ReportLLMModel:         "",
		DefaultDeviceProfile: store.DeviceProfile{
			OS:       "Mac OS 26.5.2; arm64",
			Terminal: "iTerm.app/3.6.11",
		},
		ContentAuditMode:         AuditWarn,
		ContentAuditRules:        DefaultContentRules(),
		RiskMode:                 RiskStandard,
		AllowNonCodex:            false,
		DeviceConfirmHours:       24,
		ConcurrencyWindowMinutes: 10,
		NewDeviceGraceMinutes:    30,
		MaxActiveDevices:         3,
		RequireCodexInstallID:    false,
		DefaultCodexModel:        "gpt-6-luna",
		MaxAccountMembers:        3,
	}
}

// mergeDefaults fills zero-valued fields with defaults (so partial DB rows stay valid).
func mergeDefaults(s Settings) Settings {
	d := Defaults()
	if s.DefaultMaxConcurrency == 0 {
		s.DefaultMaxConcurrency = d.DefaultMaxConcurrency
	}
	if s.QueueTimeoutSeconds == 0 {
		s.QueueTimeoutSeconds = d.QueueTimeoutSeconds
	}
	if s.RetentionDays == 0 {
		s.RetentionDays = d.RetentionDays
	}
	if s.CodexProfile.Version == "" {
		s.CodexProfile.Version = d.CodexProfile.Version
	}
	if s.MaxAccountMembers <= 0 {
		s.MaxAccountMembers = d.MaxAccountMembers
	}
	if strings.TrimSpace(s.DefaultCodexModel) == "" {
		s.DefaultCodexModel = d.DefaultCodexModel
	}
	if s.CodexProfile.UATemplate == "" {
		s.CodexProfile.UATemplate = d.CodexProfile.UATemplate
	}
	if s.CodexProfile.Originator == "" {
		s.CodexProfile.Originator = d.CodexProfile.Originator
	}
	if len(s.ClientRules) == 0 || isOldDefaultClientRules(s.ClientRules) {
		s.ClientRules = d.ClientRules
	}
	if len(s.RedactionRules) == 0 {
		s.RedactionRules = d.RedactionRules
	}
	if s.ModelMismatchThreshold == 0 {
		s.ModelMismatchThreshold = d.ModelMismatchThreshold
	}
	if s.DefaultDeviceProfile.OS == "" {
		s.DefaultDeviceProfile.OS = d.DefaultDeviceProfile.OS
	}
	if s.DefaultDeviceProfile.Terminal == "" {
		s.DefaultDeviceProfile.Terminal = d.DefaultDeviceProfile.Terminal
	}
	if s.RiskMode == "" {
		s.RiskMode = d.RiskMode
	}
	if s.DeviceConfirmHours == 0 {
		s.DeviceConfirmHours = d.DeviceConfirmHours
	}
	if s.ConcurrencyWindowMinutes == 0 {
		s.ConcurrencyWindowMinutes = d.ConcurrencyWindowMinutes
	}
	if s.NewDeviceGraceMinutes == 0 {
		s.NewDeviceGraceMinutes = d.NewDeviceGraceMinutes
	}
	if s.MaxActiveDevices == 0 {
		s.MaxActiveDevices = d.MaxActiveDevices
	}
	if s.ContentAuditMode == "" {
		s.ContentAuditMode = d.ContentAuditMode
	}
	if s.ContentAuditRules == nil {
		s.ContentAuditRules = d.ContentAuditRules
	}
	return s
}

// ParseNetworks parses CIDRs or single IPs (as /32 or /128).
func ParseNetworks(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "/") {
			ip := net.ParseIP(v)
			if ip == nil {
				return nil, fmt.Errorf("office network %q is not an IP or CIDR", v)
			}
			if ip.To4() != nil {
				v += "/32"
			} else {
				v += "/128"
			}
		}
		_, n, err := net.ParseCIDR(v)
		if err != nil {
			return nil, fmt.Errorf("office network %q is not an IP or CIDR", raw)
		}
		out = append(out, n)
	}
	return out, nil
}

// Validate checks bounds and regex compilability.
func Validate(s Settings) error {
	if s.DefaultMaxConcurrency < 1 || s.DefaultMaxConcurrency > 32 {
		return fmt.Errorf("default_max_concurrency must be 1..32")
	}
	if s.QueueTimeoutSeconds < 1 || s.QueueTimeoutSeconds > 600 {
		return fmt.Errorf("queue_timeout_seconds must be 1..600")
	}
	if s.RetentionDays < 7 || s.RetentionDays > 3650 {
		return fmt.Errorf("retention_days must be 7..3650")
	}
	if s.ModelMismatchThreshold < 1 {
		return fmt.Errorf("model_mismatch_threshold must be >= 1")
	}
	switch s.RiskMode {
	case RiskLax, RiskStandard, RiskStrict:
	default:
		return fmt.Errorf("risk_mode must be lax, standard or strict")
	}
	if s.DeviceConfirmHours < 1 || s.DeviceConfirmHours > 720 {
		return fmt.Errorf("device_confirm_hours must be 1..720")
	}
	if s.ConcurrencyWindowMinutes < 1 || s.ConcurrencyWindowMinutes > 240 {
		return fmt.Errorf("concurrency_window_minutes must be 1..240")
	}
	if s.NewDeviceGraceMinutes < 1 || s.NewDeviceGraceMinutes > 1440 {
		return fmt.Errorf("new_device_grace_minutes must be 1..1440")
	}
	if s.MaxActiveDevices < 1 || s.MaxActiveDevices > 50 {
		return fmt.Errorf("max_active_devices must be 1..50")
	}
	if s.MaxAccountMembers < 1 || s.MaxAccountMembers > 10 {
		return fmt.Errorf("max_account_members must be 1..10")
	}
	if m := strings.TrimSpace(s.DefaultCodexModel); m != "" && (len(m) > 100 || strings.ContainsAny(m, " /\"\n")) {
		return fmt.Errorf("default_codex_model must be a model id like gpt-6-luna")
	}
	nets, err := ParseNetworks(s.OfficeNetworks)
	if err != nil {
		return err
	}
	if s.OfficeOnly && len(nets) == 0 {
		return fmt.Errorf("office_only needs at least one office network")
	}
	switch s.ContentAuditMode {
	case AuditOff, AuditWarn, AuditBlock:
	default:
		return fmt.Errorf("content_audit_mode must be off, warn or block")
	}
	for _, r := range s.ContentAuditRules {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("content audit rules need a name")
		}
		if strings.TrimSpace(r.Pattern) == "" {
			// An empty pattern matches every message (in block mode: every request).
			return fmt.Errorf("content audit rule %q has an empty pattern", r.Name)
		}
		if _, err := regexp.Compile("(?i)" + r.Pattern); err != nil {
			return fmt.Errorf("content audit rule %q: %w", r.Name, err)
		}
	}
	for _, r := range s.ClientRules {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("client rule %q: %w", r.Header, err)
		}
	}
	for _, r := range s.RedactionRules {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("redaction rule %q: %w", r.Name, err)
		}
	}
	return nil
}

// Service reads/writes Settings with a short cache.
type Service struct {
	st     *store.Store
	mu     sync.Mutex
	cache  Settings
	at     time.Time
	loaded bool // cache holds a value actually read from (or written to) the DB
	ttl    time.Duration
}

// NewService creates a settings service.
func NewService(st *store.Store) *Service {
	return &Service{st: st, ttl: 5 * time.Second}
}

// Get returns current settings, refreshing the cache at most every ttl.
func (s *Service) Get(ctx context.Context) Settings {
	s.mu.Lock()
	if time.Since(s.at) < s.ttl && !s.at.IsZero() {
		c := s.cache
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()

	cur := Defaults()
	raw, ok, err := s.st.GetSettingRaw(ctx, settingsKey)
	if err != nil {
		// DB unreachable: keep serving the last good settings instead of
		// silently reverting to defaults; retry after another ttl.
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.loaded {
			s.at = time.Now()
			return s.cache
		}
		return cur
	}
	if ok {
		var stored Settings
		if json.Unmarshal(raw, &stored) == nil {
			cur = mergeDefaults(stored)
		}
	}
	s.mu.Lock()
	s.cache = cur
	s.at = time.Now()
	s.loaded = true
	s.mu.Unlock()
	return cur
}

// Put validates and persists settings, invalidating the cache.
func (s *Service) Put(ctx context.Context, in Settings) error {
	in = mergeDefaults(in)
	if err := Validate(in); err != nil {
		return err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if err := s.st.PutSettingRaw(ctx, settingsKey, raw); err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = in
	s.at = time.Now()
	s.loaded = true
	s.mu.Unlock()
	return nil
}

// isOldDefaultClientRules reports whether rules are the earlier, case-sensitive
// defaults, which turned away the Codex desktop app; saved copies are upgraded.
func isOldDefaultClientRules(rules []ClientRule) bool {
	old := []ClientRule{
		{Kind: "codex", Header: "User-Agent", Pattern: `^(codex[-_ ]|codex_cli_rs|codex-tui|codex_vscode|codex_exec)`},
		{Kind: "codex", Header: "Originator", Pattern: `^codex`},
	}
	if len(rules) != len(old) {
		return false
	}
	for i := range old {
		if rules[i] != old[i] {
			return false
		}
	}
	return true
}
