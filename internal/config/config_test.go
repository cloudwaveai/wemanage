package config

import (
	"encoding/base64"
	"testing"
)

func validEnv(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString(key))
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
}

func TestLoadRequiresMasterKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("MASTER_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when MASTER_KEY is missing")
	}
}

func TestLoadMasterKeyWrongSize(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if _, err := Load(); err == nil {
		t.Fatal("expected error when MASTER_KEY decodes to 16 bytes")
	}
}

func TestLoadMasterKeyNotBase64(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("MASTER_KEY", "not!!base64!!")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when MASTER_KEY is not base64")
	}
}

func TestLoadDefaultsAndLists(t *testing.T) {
	validEnv(t)
	t.Setenv("BOOTSTRAP_SUPER_ADMINS", " ou_a , ou_b ,, ou_c ")
	t.Setenv("DEV_LOGIN", "TRUE")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.MasterKey) != 32 {
		t.Fatalf("master key len = %d", len(c.MasterKey))
	}
	if got := c.BootstrapSuperAdmins; len(got) != 3 || got[0] != "ou_a" || got[2] != "ou_c" {
		t.Fatalf("bootstrap admins = %v", got)
	}
	if !c.DevLogin {
		t.Fatal("DevLogin should be true")
	}
	if c.Timezone == nil || c.Timezone.String() != "Asia/Shanghai" {
		t.Fatalf("timezone = %v", c.Timezone)
	}
	if c.ListenAddr != ":8080" {
		t.Fatalf("default listen addr = %q", c.ListenAddr)
	}
}

func TestLoadURLSafeMasterKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("MASTER_KEY", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if _, err := Load(); err != nil {
		t.Fatalf("url-safe master key should load: %v", err)
	}
}

func TestLoadBadTimezone(t *testing.T) {
	validEnv(t)
	t.Setenv("TIMEZONE", "Not/AZone")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for bad timezone")
	}
}
