// Package store is the data-access layer over PostgreSQL for all aggregates.
package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Common errors.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
	// ErrAccountFull: the account already has as many employees as allowed.
	ErrAccountFull = errors.New("store: account full")
)

// BlobBackend stores opaque (already-compressed) blob bytes outside Postgres,
// e.g. in S3/R2. Keys are content-addressed. Implementations live in
// internal/blobstore; a nil backend means blobs are kept inline in Postgres.
type BlobBackend interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, keys []string) error
}

// Store wraps a pgx pool and an optional external blob backend.
type Store struct {
	Pool        *pgxpool.Pool
	blobBackend BlobBackend
}

// New creates a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// SetBlobBackend switches blob byte storage to an external backend (S3/R2).
// When set, new blobs store their bytes in the backend and only metadata in
// Postgres; a nil backend (default) keeps bytes inline in the blobs table.
func (s *Store) SetBlobBackend(b BlobBackend) { s.blobBackend = b }

// DeviceProfile is the stable per-account fingerprint used for non-Codex clients.
type DeviceProfile struct {
	OS             string `json:"os"`
	Terminal       string `json:"terminal"`
	InstallationID string `json:"installation_id"`
}

// Employee is a synced Feishu user.
type Employee struct {
	ID            int64
	FeishuOpenID  string
	FeishuUnionID string
	FeishuUserID  string
	Name          string
	Email         string
	AvatarURL     string
	Status        string
	IsSuperAdmin  bool
	NoticeAckAt   *time.Time
	CreatedAt     time.Time
	// NoticeAckVersion is the usage-notice version the employee last acknowledged.
	NoticeAckVersion int
	// FrozenAt is set when risk controls froze the employee; only an admin can clear it.
	FrozenAt     *time.Time
	FrozenReason string
	// RemoteAllowed exempts the employee from "office networks only".
	RemoteAllowed bool
	// WeeklyReportOff: the employee turned their weekly AI recap off.
	WeeklyReportOff bool
	// QuotaAlert: the employee asked to be told when their quota reaches 80%.
	QuotaAlert bool
	// IsService: not a person but a service (e.g. the New API platform),
	// created by administrators; no Feishu, devices or weekly report.
	IsService bool
}

// Account is an upstream ChatGPT subscription account.
type Account struct {
	ID               int64
	CPAAuthName      string
	Email            string
	PlanType         string
	OwnerType        string
	IPGroupID        int64
	Status           string
	NeedsLogin       bool
	RateLimitedUntil *time.Time
	MaxConcurrency   *int
	DeviceProfile    DeviceProfile
	CPADrift         string
	CreatedAt        time.Time
	// Provider is the CPA auth-file type: "codex" (ChatGPT) or e.g. "kimi".
	Provider string
	// AllowedModels lists the models the account may be used with; nil
	// means all of them.
	AllowedModels []string
	// AllowThirdParty lets clients other than Codex (OpenCode, Command
	// Code, …) use the account.
	AllowThirdParty bool
}

// AllowsModel reports whether the account may be used with model ("" is any).
func (a Account) AllowsModel(model string) bool {
	if a.AllowedModels == nil || model == "" {
		return true
	}
	for _, m := range a.AllowedModels {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}

// Provider values.
const (
	ProviderCodex = "codex"
	ProviderKimi  = "kimi"
)

// IsChatGPT reports whether the account is a ChatGPT/Codex subscription (the
// quota headers and degradation heuristics only apply to these).
func (a Account) IsChatGPT() bool { return a.Provider == "" || a.Provider == ProviderCodex }

// Prefix is the CPA model/credential prefix bound to this account.
func (a Account) Prefix() string { return "acct-" + itoa(a.ID) }

// IPGroup is a stable egress proxy shared by up to Capacity accounts.
type IPGroup struct {
	ID               int64
	Name             string
	ProxyURL         string // decrypted
	Capacity         int
	ExpectedEgressIP string
	LastEgressIP     string
	LastCheckError   string
	LastCheckedAt    *time.Time
	AccountCount     int
}

// APIKey is an issued employee key (hash stored, never plaintext).
type APIKey struct {
	ID         int64
	EmployeeID int64
	KeyPrefix  string
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// KeyPrincipal is the resolved identity behind an API key, for the data plane.
type KeyPrincipal struct {
	KeyID            int64
	EmployeeID       int64
	EmployeeOpenID   string
	EmployeeStatus   string
	NoticeAcked      bool // acknowledged any version
	NoticeAckVersion int
	Frozen           bool
	FrozenReason     string
	RemoteAllowed    bool
	IsService        bool // a service account (see Employee.IsService)
	APIChannelID     *int64
	// Accounts are the employee's accounts, oldest binding first.
	Accounts []Account
}

// Account returns the employee's account with the given id.
func (p KeyPrincipal) Account(id int64) (Account, bool) {
	for _, a := range p.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

// AuditEntry is a single append-only audit record.
type AuditEntry struct {
	ActorID    *int64
	Action     string
	TargetType string
	TargetID   string
	Detail     map[string]any
	IP         string
}

// Signal is an open/closed account health signal.
type Signal struct {
	ID         int64
	AccountID  int64
	Kind       string
	Detail     map[string]any
	At         time.Time
	ResolvedAt *time.Time
	NotifiedAt *time.Time
}

// QuotaSnapshot captures upstream quota usage windows at a point in time.
type QuotaSnapshot struct {
	AccountID              int64
	At                     time.Time
	PrimaryUsedPct         *float64
	PrimaryWindowMinutes   *int
	PrimaryResetAt         *time.Time
	SecondaryUsedPct       *float64
	SecondaryWindowMinutes *int
	SecondaryResetAt       *time.Time
}

// ObservedClient tracks real client UA/Version seen from Codex traffic.
type ObservedClient struct {
	UserAgent  string
	Originator string
	Version    string
	FirstSeen  time.Time
	LastSeen   time.Time
	Hits       int64
}

func itoa(v int64) string {
	// small helper avoiding fmt import churn in hot path
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// queryer is the shared subset of pgxpool.Pool and pgx.Tx used by stores.
type queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// tx runs fn in a transaction, committing on nil error.
func (s *Store) tx(ctx context.Context, fn func(q queryer) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ queryer = (*pgxpool.Pool)(nil)

// ModelInfo is a model an upstream account can serve.
type ModelInfo struct {
	ID      string
	Name    string // display name, when known
	Created int64  // unix seconds the model was released, when known
}
