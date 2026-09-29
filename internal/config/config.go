// Package config loads and validates gateway configuration from environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration for the gateway.
type Config struct {
	ListenAddr           string
	DatabaseURL          string
	PublicBaseURL        string
	CPABaseURL           string
	CPAAPIKey            string
	CPAManagementKey     string
	MasterKey            []byte // 32 bytes for AES-256-GCM
	FeishuAppID          string
	FeishuAppSecret      string
	FeishuBaseURL        string
	FeishuLoginScope     string
	BootstrapSuperAdmins []string
	ReportLLMBaseURL     string
	ReportLLMAPIKey      string
	ReportLLMModel       string
	SpoolDir             string
	EgressEchoURL        string
	DevLogin             bool
	Timezone             *time.Location

	// Risk controls.
	GeoIPDB           string // optional ip2region .xdb path
	TrustProxyHeaders bool   // trust X-Forwarded-For from private peers (Caddy)

	// Blob storage backend: "postgres" (default) or "s3".
	BlobBackend string
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment, applying defaults and validating.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:        getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		PublicBaseURL:     strings.TrimRight(getenv("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		CPABaseURL:        strings.TrimRight(getenv("CPA_BASE_URL", "http://cpa:8317"), "/"),
		CPAAPIKey:         strings.TrimSpace(os.Getenv("CPA_API_KEY")),
		CPAManagementKey:  strings.TrimSpace(os.Getenv("CPA_MANAGEMENT_KEY")),
		FeishuAppID:       strings.TrimSpace(os.Getenv("FEISHU_APP_ID")),
		FeishuAppSecret:   strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET")),
		FeishuBaseURL:     strings.TrimRight(getenv("FEISHU_BASE_URL", "https://open.feishu.cn"), "/"),
		FeishuLoginScope:  strings.TrimSpace(os.Getenv("FEISHU_LOGIN_SCOPE")),
		ReportLLMBaseURL:  strings.TrimRight(getenv("REPORT_LLM_BASE_URL", "https://api.openai.com/v1"), "/"),
		ReportLLMAPIKey:   strings.TrimSpace(os.Getenv("REPORT_LLM_API_KEY")),
		ReportLLMModel:    getenv("REPORT_LLM_MODEL", "gpt-5-mini"),
		SpoolDir:          getenv("SPOOL_DIR", "./spool"),
		EgressEchoURL:     getenv("EGRESS_ECHO_URL", "https://api.ipify.org?format=text"),
		DevLogin:          strings.EqualFold(strings.TrimSpace(os.Getenv("DEV_LOGIN")), "true"),
		GeoIPDB:           strings.TrimSpace(os.Getenv("GEOIP_DB")),
		TrustProxyHeaders: !strings.EqualFold(strings.TrimSpace(os.Getenv("TRUST_PROXY_HEADERS")), "false"),
		BlobBackend:       strings.ToLower(getenv("BLOB_BACKEND", "postgres")),
		S3Endpoint:        strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
		S3Region:          strings.TrimSpace(os.Getenv("S3_REGION")),
		S3Bucket:          strings.TrimSpace(os.Getenv("S3_BUCKET")),
		S3AccessKey:       strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
		S3SecretKey:       strings.TrimSpace(os.Getenv("S3_SECRET_KEY")),
		S3UseSSL:          !strings.EqualFold(strings.TrimSpace(os.Getenv("S3_USE_SSL")), "false"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}

	rawKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	if rawKey == "" {
		return nil, fmt.Errorf("config: MASTER_KEY is required (base64 of 32 bytes)")
	}
	key, err := decodeMasterKey(rawKey)
	if err != nil {
		return nil, fmt.Errorf("config: MASTER_KEY: %w", err)
	}
	c.MasterKey = key

	if c.BlobBackend != "postgres" && c.BlobBackend != "s3" {
		return nil, fmt.Errorf("config: BLOB_BACKEND must be postgres or s3, got %q", c.BlobBackend)
	}
	if c.BlobBackend == "s3" {
		if c.S3Endpoint == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
			return nil, fmt.Errorf("config: BLOB_BACKEND=s3 requires S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
		}
	}

	c.BootstrapSuperAdmins = splitList(os.Getenv("BOOTSTRAP_SUPER_ADMINS"))

	tzName := getenv("TIMEZONE", "Asia/Shanghai")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("config: TIMEZONE %q: %w", tzName, err)
	}
	c.Timezone = loc

	return c, nil
}

func decodeMasterKey(raw string) ([]byte, error) {
	// Accept standard or url-safe base64, with or without padding.
	var key []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		key, err = enc.DecodeString(raw)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("not valid base64")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("must decode to exactly 32 bytes, got %d", len(key))
	}
	return key, nil
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
