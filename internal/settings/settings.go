// Package settings holds tunable, DB-backed configuration with defaults and a read cache.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	NoticeText             string              `json:"notice_text"`
	DefaultDeviceProfile   store.DeviceProfile `json:"default_device_profile"`
}

// DefaultNoticeText is shown to employees before they can obtain a key.
const DefaultNoticeText = "本服务的请求和回复会被公司记录，用于用量管理和工作分析，原文保留 180 天。继续使用即表示你已知晓。"

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
		ClientRules: []ClientRule{
			{Kind: "codex", Header: "User-Agent", Pattern: `^(codex[-_ ]|codex_cli_rs|codex-tui|codex_vscode|codex_exec)`},
			{Kind: "codex", Header: "Originator", Pattern: `^codex`},
		},
		RedactionRules:         DefaultRedactionRules(),
		ModelMismatchThreshold: 3,
		ReportEnabled:          true,
		ReportLLMModel:         "",
		NoticeText:             DefaultNoticeText,
		DefaultDeviceProfile: store.DeviceProfile{
			OS:       "Mac OS 26.5.2; arm64",
			Terminal: "iTerm.app/3.6.11",
		},
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
	if s.CodexProfile.UATemplate == "" {
		s.CodexProfile.UATemplate = d.CodexProfile.UATemplate
	}
	if s.CodexProfile.Originator == "" {
		s.CodexProfile.Originator = d.CodexProfile.Originator
	}
	if len(s.ClientRules) == 0 {
		s.ClientRules = d.ClientRules
	}
	if len(s.RedactionRules) == 0 {
		s.RedactionRules = d.RedactionRules
	}
	if s.ModelMismatchThreshold == 0 {
		s.ModelMismatchThreshold = d.ModelMismatchThreshold
	}
	if s.NoticeText == "" {
		s.NoticeText = d.NoticeText
	}
	if s.DefaultDeviceProfile.OS == "" {
		s.DefaultDeviceProfile.OS = d.DefaultDeviceProfile.OS
	}
	if s.DefaultDeviceProfile.Terminal == "" {
		s.DefaultDeviceProfile.Terminal = d.DefaultDeviceProfile.Terminal
	}
	return s
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
	st    *store.Store
	mu    sync.Mutex
	cache Settings
	at    time.Time
	ttl   time.Duration
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
	if err == nil && ok {
		var stored Settings
		if json.Unmarshal(raw, &stored) == nil {
			cur = mergeDefaults(stored)
		}
	}
	s.mu.Lock()
	s.cache = cur
	s.at = time.Now()
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
	s.mu.Unlock()
	return nil
}
