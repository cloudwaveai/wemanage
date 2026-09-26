# AI 订阅网关 V1 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付设计文档 M1–M4 全部范围的 V1：一人一号的 Codex 订阅网关，含全量日志、并发闸、指纹处理、飞书登录、管理后台、健康监测、AI 日报，并可用 Docker Compose 部署。

**Architecture:** Go 单进程网关（数据面 `/v1/*` + 管理面 `/api/*` + 内嵌 React SPA + 定时任务）位于原版 CLIProxyAPI 之前。网关负责身份、绑定、并发、指纹、日志；CPA 负责 OAuth 刷新与上游协议。PostgreSQL 存全部数据，原文按片段去重 + 会话前缀编码存储。

**Tech Stack:** Go 1.26、chi v5、pgx v5、goose v3、klauspost/compress/zstd、tidwall/gjson+sjson、google/uuid、robfig/cron v3；React 18 + Vite + TypeScript + Tailwind + TanStack Query + Recharts；PostgreSQL 16；Caddy 2；CPA 镜像 `eceasy/cli-proxy-api`（固定版本 tag）。

**Spec:** `docs/superpowers/specs/2026-09-27-ai-seat-gateway-design.md`

## Global Constraints

- Go module 路径：`aiseat`（仓库内部项目，不发布）。
- 每账号默认并发 4，排队超时默认 60 秒，均可在设置中修改；并发按账号计数。
- 原文保留 180 天；元数据、日报、审计永久保留。
- 数据面对员工返回的所有错误体均为 OpenAI 错误格式：`{"error":{"message":"...","type":"...","code":"..."}}`。
- 脱敏只作用于落库副本，绝不修改发往上游的请求。
- OAuth token 不进入网关数据库；网关只存 CPA 凭证文件名 `cpa_auth_name`。
- CPA 前缀固定为 `acct-<upstream_accounts.id>`。
- 员工 Key 格式 `sk-emp-` + 43 位 base64url（32 字节随机）；库中只存 SHA-256 与前 12 个字符。
- 时区：定时任务与"日"的边界一律使用 `Asia/Shanghai`。
- 界面文案全部中文。
- 测试数据库：`TEST_DATABASE_URL`，默认 `postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable`（`make test-db-up` 用本地已有的 `postgres:16-alpine` 镜像启动）。
- 所有 Go 测试须在 `go test -race ./...` 下通过。
- 本机磁盘紧张（约 15 GB 可用）：不拉新镜像，不引入重型依赖。

## 与 Spec 的有意偏差（已评估）

1. `requests` 表 V1 不做按月分区：50 人规模一年约 2000 万行，普通 B-tree 索引足够；分区会让保留清理与主键设计复杂化。超过 5000 万行再迁移。
2. 请求片段不建 `request_segments` 行表（估算每天 500 万行，180 天近 10 亿行），改为在 `requests` 行上用"会话前缀编码"存哈希列表（见 Task 6）。

---

## 文件结构

```
cmd/gateway/main.go                 子命令：serve | migrate | admin (bootstrap/issue-key/import-account)
internal/config/config.go           环境变量解析与校验
internal/db/db.go                   连接池、迁移（goose + embed）
internal/db/migrations/*.sql        表结构
internal/testutil/testdb.go         每个测试独立 schema 的测试库
internal/secure/secure.go           AES-GCM 加解密、随机串、Key 生成与哈希
internal/store/*.go                 各聚合的仓储（employees, accounts, keys, ipgroups, settings, audit, sessions, requests, blobs, signals, quota, reports, websessions, observed）
internal/settings/settings.go       设置结构、默认值、读取缓存
internal/proxy/limiter.go           每账号 FIFO 并发闸
internal/proxy/clientkind.go        客户端识别
internal/proxy/fingerprint.go       请求头处理、设备档案、UA 解析
internal/proxy/principal.go         Key → 员工/账号 解析与缓存
internal/proxy/handler.go           /v1/* 数据面
internal/capture/sse.go             SSE / JSON 响应解析（用量、模型、输出、TTFT）
internal/capture/segment.go         请求/响应拆片段、规范化、哈希
internal/capture/redact.go          脱敏规则
internal/capture/encode.go          会话前缀编码、还原
internal/capture/texts.go           抽取检索与日报用文本
internal/capture/recorder.go        异步批量落库 + spool
internal/health/signals.go          降智信号、额度快照、账号状态计算
internal/health/egress.go           出口 IP 检查
internal/health/codexversion.go     Codex 版本检查与观测客户端
internal/cpa/client.go              CPA 管理 API 客户端
internal/cpa/sync.go                CPA 一致性同步
internal/cpa/onboard.go             添加账号（OAuth / 导入）
internal/feishu/client.go           飞书：token、OAuth、通讯录、消息
internal/feishu/sync.go             通讯录同步到 employees/departments
internal/web/session.go             Cookie 会话、CSRF
internal/web/rbac.go                权限范围计算
internal/web/router.go              /api/* 路由装配
internal/web/api_*.go               各组 API handler
internal/report/collect.go          日报素材收集与统计
internal/report/llm.go              OpenAI 兼容 chat 客户端
internal/report/daily.go            生成与推送
internal/jobs/jobs.go               定时任务注册
internal/jobs/retention.go          180 天清理
internal/notify/notify.go           飞书告警（去重）
internal/webui/embed.go             go:embed dist
web/                                 React 源码（vite outDir → ../internal/webui/dist）
deploy/                              docker-compose.yml、Caddyfile、cpa/config.yaml.tmpl、init.sh、.env.example
test/e2e/                            真实 CPA（源码构建）+ 模拟上游 的端到端验证
docs/                                部署、运维、员工指南、验证报告
```

---

### Task 1: 项目骨架、配置、数据库与测试库

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`, `cmd/gateway/main.go`（仅 `migrate` 子命令与占位 `serve`）
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `internal/db/db.go`, `internal/db/migrations/00001_init.sql`
- Create: `internal/testutil/testdb.go`, `internal/db/db_test.go`
- Create: `deploy/docker-compose.test.yml`（仅 postgres:16-alpine，端口 55432）

**Interfaces:**
- Produces:
  - `config.Load() (*config.Config, error)`，`Config` 字段：`ListenAddr, DatabaseURL, PublicBaseURL, CPABaseURL, CPAAPIKey, CPAManagementKey, MasterKey []byte(32), FeishuAppID, FeishuAppSecret, FeishuBaseURL, BootstrapSuperAdmins []string, ReportLLMBaseURL, ReportLLMAPIKey, ReportLLMModel, SpoolDir, DevLogin bool, Timezone *time.Location`
  - `db.Open(ctx, url string) (*pgxpool.Pool, error)`，`db.Migrate(ctx, pool) error`
  - `testutil.NewDB(t) *pgxpool.Pool`：创建随机 schema，`search_path=<schema>,public`，执行迁移，`t.Cleanup` 删除 schema

- [ ] **Step 1: 初始化模块与依赖**

```bash
cd /Users/auntlee/Desktop/workspace/ai-seat-gateway
go mod init aiseat
go get github.com/go-chi/chi/v5 github.com/jackc/pgx/v5 github.com/pressly/goose/v3 \
  github.com/klauspost/compress github.com/tidwall/gjson github.com/tidwall/sjson \
  github.com/google/uuid github.com/robfig/cron/v3
```

- [ ] **Step 2: 写迁移 `internal/db/migrations/00001_init.sql`**

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE employees (
  id BIGSERIAL PRIMARY KEY,
  feishu_open_id TEXT UNIQUE,
  feishu_union_id TEXT NOT NULL DEFAULT '',
  feishu_user_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  is_super_admin BOOLEAN NOT NULL DEFAULT FALSE,
  notice_ack_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE departments (
  id BIGSERIAL PRIMARY KEY,
  feishu_dept_id TEXT UNIQUE NOT NULL,
  parent_feishu_dept_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  leader_open_ids TEXT[] NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employee_departments (
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  department_id BIGINT NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
  PRIMARY KEY (employee_id, department_id)
);

CREATE TABLE ip_groups (
  id BIGSERIAL PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  proxy_url_enc BYTEA NOT NULL,
  capacity INT NOT NULL DEFAULT 10 CHECK (capacity > 0),
  expected_egress_ip TEXT NOT NULL DEFAULT '',
  last_egress_ip TEXT NOT NULL DEFAULT '',
  last_checked_at TIMESTAMPTZ,
  last_check_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE upstream_accounts (
  id BIGSERIAL PRIMARY KEY,
  cpa_auth_name TEXT UNIQUE NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  plan_type TEXT NOT NULL DEFAULT '',
  owner_type TEXT NOT NULL DEFAULT 'company' CHECK (owner_type IN ('company','employee')),
  employee_id BIGINT UNIQUE REFERENCES employees(id) ON DELETE SET NULL,
  ip_group_id BIGINT NOT NULL REFERENCES ip_groups(id),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  needs_login BOOLEAN NOT NULL DEFAULT FALSE,
  rate_limited_until TIMESTAMPTZ,
  max_concurrency INT CHECK (max_concurrency IS NULL OR max_concurrency > 0),
  device_profile JSONB NOT NULL,
  cpa_drift TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_onboardings (
  state TEXT PRIMARY KEY,
  ip_group_id BIGINT NOT NULL REFERENCES ip_groups(id),
  os_profile JSONB NOT NULL,
  known_auth_names TEXT[] NOT NULL,
  created_by BIGINT REFERENCES employees(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_account_id BIGINT REFERENCES upstream_accounts(id)
);

CREATE TABLE api_keys (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  key_hash BYTEA UNIQUE NOT NULL,
  key_prefix TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX api_keys_one_active ON api_keys(employee_id) WHERE revoked_at IS NULL;

CREATE TABLE sessions (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  account_id BIGINT REFERENCES upstream_accounts(id) ON DELETE SET NULL,
  session_key TEXT NOT NULL,
  client_kind TEXT NOT NULL,
  first_at TIMESTAMPTZ NOT NULL,
  last_at TIMESTAMPTZ NOT NULL,
  request_count INT NOT NULL DEFAULT 0,
  last_request_id UUID,
  UNIQUE (employee_id, session_key)
);

CREATE TABLE requests (
  id UUID PRIMARY KEY,
  started_at TIMESTAMPTZ NOT NULL,
  employee_id BIGINT NOT NULL,
  account_id BIGINT,
  api_key_id BIGINT,
  session_id BIGINT,
  client_kind TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  endpoint TEXT NOT NULL,
  stream BOOLEAN NOT NULL DEFAULT FALSE,
  model_requested TEXT NOT NULL DEFAULT '',
  model_responded TEXT NOT NULL DEFAULT '',
  reasoning_effort TEXT NOT NULL DEFAULT '',
  status_code INT NOT NULL,
  error_type TEXT NOT NULL DEFAULT '',
  error_body TEXT NOT NULL DEFAULT '',
  queued_ms INT NOT NULL DEFAULT 0,
  ttft_ms INT,
  duration_ms INT NOT NULL DEFAULT 0,
  tokens_input BIGINT NOT NULL DEFAULT 0,
  tokens_cached BIGINT NOT NULL DEFAULT 0,
  tokens_output BIGINT NOT NULL DEFAULT 0,
  tokens_reasoning BIGINT NOT NULL DEFAULT 0,
  req_prefix_of UUID,
  req_prefix_len INT NOT NULL DEFAULT 0,
  req_chain_depth INT NOT NULL DEFAULT 0,
  req_hashes BYTEA,
  req_input_start INT NOT NULL DEFAULT 0,
  resp_hashes BYTEA,
  bodies_purged BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX requests_emp_time ON requests(employee_id, started_at DESC);
CREATE INDEX requests_acct_time ON requests(account_id, started_at DESC);
CREATE INDEX requests_session_time ON requests(session_id, started_at DESC);
CREATE INDEX requests_time ON requests(started_at DESC);
CREATE INDEX requests_prefix_of ON requests(req_prefix_of) WHERE req_prefix_of IS NOT NULL;

CREATE TABLE blobs (
  hash BYTEA PRIMARY KEY,
  kind TEXT NOT NULL,
  data_zstd BYTEA NOT NULL,
  raw_size INT NOT NULL,
  last_referenced_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX blobs_last_ref ON blobs(last_referenced_at);

CREATE TABLE request_texts (
  id BIGSERIAL PRIMARY KEY,
  request_id UUID NOT NULL,
  employee_id BIGINT NOT NULL,
  session_id BIGINT,
  started_at TIMESTAMPTZ NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('user','assistant')),
  text TEXT NOT NULL
);
CREATE INDEX request_texts_trgm ON request_texts USING gin (text public.gin_trgm_ops);
CREATE INDEX request_texts_emp_time ON request_texts(employee_id, started_at);
CREATE INDEX request_texts_request ON request_texts(request_id);

CREATE TABLE account_signals (
  id BIGSERIAL PRIMARY KEY,
  account_id BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}',
  at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  notified_at TIMESTAMPTZ
);
CREATE INDEX account_signals_open ON account_signals(account_id, kind, at DESC);

CREATE TABLE quota_snapshots (
  account_id BIGINT NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
  at TIMESTAMPTZ NOT NULL,
  primary_used_pct REAL,
  primary_window_minutes INT,
  primary_reset_at TIMESTAMPTZ,
  secondary_used_pct REAL,
  secondary_window_minutes INT,
  secondary_reset_at TIMESTAMPTZ,
  PRIMARY KEY (account_id, at)
);

CREATE TABLE daily_reports (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  date DATE NOT NULL,
  stats JSONB NOT NULL,
  summary_md TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  pushed_at TIMESTAMPTZ,
  UNIQUE (employee_id, date)
);

CREATE TABLE audit_logs (
  id BIGSERIAL PRIMARY KEY,
  actor_employee_id BIGINT,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id TEXT NOT NULL DEFAULT '',
  detail JSONB NOT NULL DEFAULT '{}',
  ip TEXT NOT NULL DEFAULT '',
  at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_at ON audit_logs(at DESC);

-- +goose StatementBegin
CREATE FUNCTION audit_logs_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER audit_logs_no_change BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE web_sessions (
  id TEXT PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE observed_clients (
  user_agent TEXT PRIMARY KEY,
  originator TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  first_seen TIMESTAMPTZ NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL,
  hits BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE oauth_states (
  state TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP SCHEMA IF EXISTS public CASCADE;
```

- [ ] **Step 3: 写 `internal/testutil/testdb.go`**

```go
package testutil

func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("test database unreachable (%v); run `make test-db-up`", err)
	}
	schema := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil { t.Fatal(err) }
	if _, err := admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public"); err != nil { t.Fatal(err) }
	cfg, _ := pgxpool.ParseConfig(url)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil { t.Fatal(err) }
	if err := db.Migrate(ctx, pool); err != nil { t.Fatal(err) }
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	return pool
}
```

`db.Migrate` 使用 `goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrationsFS)`；`CREATE EXTENSION` 在迁移中重复执行无副作用。

- [ ] **Step 4: 写测试 `internal/db/db_test.go`**：迁移后 `audit_logs` 插入成功、`UPDATE audit_logs` 报错 `append-only`；`api_keys_one_active` 阻止同一员工两个未撤销 Key。
- [ ] **Step 5: `config_test.go`**：缺 `DATABASE_URL`/`MASTER_KEY` 报错；`MASTER_KEY` 必须 base64 解码为 32 字节；`BOOTSTRAP_SUPER_ADMINS` 逗号分隔去空白；`DEV_LOGIN=true` 解析为 true。
- [ ] **Step 6: `make test-db-up && go test -race ./...` 通过；提交** `feat: project skeleton, schema and test harness`

---

### Task 2: 安全原语与仓储层

**Files:**
- Create: `internal/secure/secure.go` + `_test.go`
- Create: `internal/store/store.go`（`type Store struct{ Pool *pgxpool.Pool }` 与 `ErrNotFound`）
- Create: `internal/store/{employees,accounts,keys,ipgroups,settings,audit,websessions,signals,quota,observed}.go` + 对应 `_test.go`
- Create: `internal/settings/settings.go` + `_test.go`

**Interfaces:**
- Produces（`secure`）：
  - `NewAPIKey() (plain string, hash []byte, prefix string)`；`HashAPIKey(plain string) []byte`
  - `RandomToken(nBytes int) string`（base64url 无填充）
  - `type Box struct{...}`；`NewBox(key []byte) (*Box, error)`；`(*Box).Seal(plain []byte) []byte`；`(*Box).Open(sealed []byte) ([]byte, error)`
- Produces（`store` 类型）：

```go
type Employee struct {
	ID int64; FeishuOpenID, FeishuUnionID, FeishuUserID, Name, Email, AvatarURL, Status string
	IsSuperAdmin bool; NoticeAckAt *time.Time; CreatedAt time.Time
}
type DeviceProfile struct {
	OS string `json:"os"`; Terminal string `json:"terminal"`; InstallationID string `json:"installation_id"`
}
type Account struct {
	ID int64; CPAAuthName, Email, PlanType, OwnerType string; EmployeeID *int64; IPGroupID int64
	Status string; NeedsLogin bool; RateLimitedUntil *time.Time; MaxConcurrency *int
	DeviceProfile DeviceProfile; CPADrift string; CreatedAt time.Time
}
func (a Account) Prefix() string { return fmt.Sprintf("acct-%d", a.ID) }
type IPGroup struct {
	ID int64; Name string; ProxyURL string /* 解密后 */; Capacity int
	ExpectedEgressIP, LastEgressIP, LastCheckError string; LastCheckedAt *time.Time; AccountCount int
}
type APIKey struct { ID, EmployeeID int64; KeyPrefix string; CreatedAt time.Time; RevokedAt, LastUsedAt *time.Time }
type AuditEntry struct { ActorID *int64; Action, TargetType, TargetID string; Detail map[string]any; IP string }
type Signal struct { ID, AccountID int64; Kind string; Detail map[string]any; At time.Time; ResolvedAt, NotifiedAt *time.Time }
type QuotaSnapshot struct {
	AccountID int64; At time.Time
	PrimaryUsedPct *float64; PrimaryWindowMinutes *int; PrimaryResetAt *time.Time
	SecondaryUsedPct *float64; SecondaryWindowMinutes *int; SecondaryResetAt *time.Time
}
```

- Produces（`store` 方法，全部 `ctx context.Context` 首参）：
  - Employees：`UpsertEmployeeByOpenID(ctx, e Employee) (Employee, error)`、`GetEmployee(ctx, id)`、`GetEmployeeByOpenID(ctx, openID)`、`ListEmployees(ctx, f EmployeeFilter) ([]EmployeeRow, error)`（`EmployeeRow` 含绑定账号 ID/邮箱、有效 Key 前缀、部门名）、`SetEmployeeStatus`、`SetSuperAdmin`、`AckNotice`、`DisableEmployeesNotIn(ctx, openIDs []string) (int64, error)`
  - Accounts：`CreateAccount(ctx, a Account) (Account, error)`、`GetAccount`、`ListAccounts(ctx) ([]AccountRow, error)`（含员工名、IP 组名）、`BindAccount(ctx, accountID, employeeID int64) error`（员工已有账号或账号已绑定 → `ErrConflict`）、`UnbindAccount`、`UpdateAccount(ctx, id, AccountPatch)`、`SetNeedsLogin(ctx, id, bool)`、`SetRateLimitedUntil(ctx, id, *time.Time)`、`SetCPADrift(ctx, id, string)`、`CountAccountsInGroup`
  - Keys：`IssueKey(ctx, employeeID) (plain string, APIKey, error)`（同一事务内撤销旧 Key）、`RevokeKeys(ctx, employeeID) error`、`ActiveKey(ctx, employeeID) (*APIKey, error)`、`LookupKey(ctx, hash []byte) (*KeyPrincipal, error)`、`TouchKey(ctx, keyID)`
    - `KeyPrincipal{KeyID, EmployeeID int64; EmployeeStatus string; NoticeAcked bool; Account *Account}`
  - IP 组：`CreateIPGroup(ctx, name, proxyURL string, capacity int) (IPGroup, error)`（用 `Box` 加密）、`ListIPGroups`、`GetIPGroup`、`UpdateIPGroup`、`RecordEgressCheck(ctx, id, ip, errMsg string)`
  - Settings：`GetSettingRaw(ctx, key) (json.RawMessage, bool, error)`、`PutSettingRaw(ctx, key, value json.RawMessage)`
  - Audit：`Audit(ctx, e AuditEntry) error`、`ListAudit(ctx, f AuditFilter) ([]AuditRow, error)`
  - WebSessions：`CreateWebSession(ctx, employeeID, ttl) (id, csrf string, err error)`、`GetWebSession(ctx, id) (*WebSession, error)`（过期返回 `ErrNotFound`）、`DeleteWebSession`
  - Signals：`AddSignal(ctx, accountID, kind string, detail map[string]any) (Signal, error)`、`OpenSignals(ctx, accountID) ([]Signal, error)`、`CountSignalsSince(ctx, accountID, kind, since) (int, error)`、`HasOpenSignal(ctx, accountID, kind) (bool, error)`、`ResolveSignal(ctx, id)`、`UnnotifiedSignals(ctx) ([]Signal, error)`、`MarkNotified(ctx, id)`
  - Quota：`InsertQuota(ctx, q QuotaSnapshot)`、`LatestQuota(ctx, accountID) (*QuotaSnapshot, error)`
  - Observed：`UpsertObservedClients(ctx, []ObservedClient)`、`TopObservedClients(ctx, since, limit) ([]ObservedClient, error)`
- Produces（`settings`）：

```go
type CodexProfile struct { Version, UATemplate, Originator string }
type ClientRule struct { Kind, Header, Pattern string }
type RedactionRule struct { Name, Pattern string }
type Settings struct {
	DefaultMaxConcurrency int; QueueTimeoutSeconds int; RetentionDays int
	CodexProfile CodexProfile; ClientRules []ClientRule; RedactionRules []RedactionRule
	ModelMismatchThreshold int; ReportEnabled bool; ReportLLMModel string; NoticeText string
	DefaultDeviceProfile store.DeviceProfile
}
func Defaults() Settings
type Service struct{...}
func NewService(st *store.Store) *Service
func (s *Service) Get(ctx) Settings            // 5 秒缓存；DB 中缺失的字段取默认值
func (s *Service) Put(ctx, Settings) error      // 校验：并发 1–32、超时 1–600、保留 7–3650、正则可编译
```

默认值：并发 4；超时 60；保留 180；`CodexProfile{Version:"0.157.1", UATemplate:"codex-tui/{version} ({os}) {terminal} (codex-tui; {version})", Originator:"codex-tui"}`；`DefaultDeviceProfile{OS:"Mac OS 26.5.2; arm64", Terminal:"iTerm.app/3.6.11"}`；识别规则 `[{codex, User-Agent, ^(codex[-_ ]|codex_cli_rs|codex-tui|codex_vscode|codex_exec)}, {codex, Originator, ^codex}]`；脱敏规则见 Task 6；降智阈值 3；日报开启；告知文案为 spec §7 的原话。

- [ ] 测试要点（每个仓储文件一个 `_test.go`，用 `testutil.NewDB`）：
  - `NewAPIKey` 前缀为 `sk-emp-`，总长 50，`HashAPIKey(plain)` 等于返回的 hash。
  - `Box` 加密后可解密；篡改一个字节 `Open` 报错；两次 Seal 同一明文结果不同。
  - `IssueKey` 第二次调用后第一个 Key 的 `LookupKey` 返回 `ErrNotFound`（已撤销）。
  - `BindAccount`：员工已绑定另一账号 → `ErrConflict`；账号已绑定他人 → `ErrConflict`。
  - `LookupKey` 返回的 `KeyPrincipal.Account` 为绑定账号；未绑定时 `Account == nil`。
  - `CreateIPGroup` 存储的 `proxy_url_enc` 不含明文代理地址。
  - `settings.Put` 拒绝不可编译的正则；`Get` 对缺失字段返回默认值。
- [ ] `go test -race ./...` 通过；提交 `feat: secure primitives, store and settings`

---

### Task 3: 每账号 FIFO 并发闸

**Files:** Create `internal/proxy/limiter.go`, `internal/proxy/limiter_test.go`

**Interfaces:**
- Produces:

```go
var ErrQueueTimeout = errors.New("queue timeout")
type Limiter struct{ ... }
func NewLimiter() *Limiter
// Acquire 阻塞直到获得名额、ctx 取消或 maxWait 到期。release 幂等。
func (l *Limiter) Acquire(ctx context.Context, accountID int64, capacity int, maxWait time.Duration) (release func(), waited time.Duration, err error)
func (l *Limiter) InFlight(accountID int64) (inflight, queued int)
```

实现：每账号 `{mu, inflight int, waiters *list.List of chan struct{}}`；release 时若有等待者则把名额直接交给队首（inflight 不变）；等待者超时/取消时从队列移除，若在移除前已被授予名额则立即转交下一个。

- [ ] 测试（`-race`）：
  - 容量 2，3 个并发 Acquire，第 3 个在前两个 release 前阻塞，release 一个后第 3 个获得。
  - FIFO：依次排队的 w1,w2,w3 按顺序获得。
  - `maxWait=50ms` 超时返回 `ErrQueueTimeout`，且不泄漏名额（之后仍可 Acquire 满容量）。
  - ctx 取消时返回 `ctx.Err()`。
  - 竞态：100 goroutine、容量 4，任意时刻观测到的并发数 ≤ 4，最终 inflight=0。
  - release 调两次不会使计数为负。
- [ ] 提交 `feat: per-account FIFO concurrency limiter`

---

### Task 4: 客户端识别与指纹处理

**Files:** Create `internal/proxy/clientkind.go`, `internal/proxy/fingerprint.go` + 测试 `fingerprint_test.go`（golden 表驱动）

**Interfaces:**
- Consumes: `settings.ClientRule`, `settings.CodexProfile`, `store.DeviceProfile`
- Produces:

```go
const KindCodex, KindOther = "codex", "other"
type Classifier struct{...}
func NewClassifier(rules []settings.ClientRule) (*Classifier, error)
func (c *Classifier) Classify(h http.Header) string

type UAParts struct{ Product, Version, OS, Terminal, Suffix string }
func ParseCodexUA(ua string) (UAParts, bool)    // 正则 ^(\S+)/(\S+) \(([^)]*)\) (\S+)(?: \((.*)\))?$
func RenderUA(p settings.CodexProfile, d store.DeviceProfile) string // 替换 {version}{os}{terminal}

type HeaderInput struct {
	Kind string; Client http.Header; Profile settings.CodexProfile; Device store.DeviceProfile
	SessionID string; CPAAPIKey string
}
// BuildUpstreamHeaders 返回发往 CPA 的请求头。
func BuildUpstreamHeaders(in HeaderInput) http.Header
// SessionUUID 由任意字符串派生稳定的 UUID 格式会话 ID（SHA-256 前 16 字节，version 位设为 4 的格式）
func SessionUUID(seed string) string
```

规则：
- 两种类型都设置 `Authorization: Bearer <CPAAPIKey>`、`Content-Type: application/json`，并保留客户端的 `Accept`。
- `codex`：复制客户端全部请求头，然后删除 `Authorization, Cookie, Host, Connection, Keep-Alive, Proxy-*, Te, Trailer, Transfer-Encoding, Upgrade, X-Forwarded-*, X-Real-Ip, Forwarded, Via, Cf-*, True-Client-Ip, X-Api-Key, Content-Length`。若客户端没有 `Session-Id`/`Session_id`，设置 `Session-Id: SessionID`。
- `other`：**只**构造白名单：`User-Agent = RenderUA`、`Originator = Profile.Originator`、`Version = Profile.Version`、`Session-Id = SessionID`、`Accept`（客户端值或 `text/event-stream`）。客户端其他任何头（如 `X-Stainless-*`、`Http-Referer`、`X-Title`）一律不带。

- [ ] golden 测试：
  - Codex CLI 请求带 `X-Forwarded-For`、`Cookie`、`X-Codex-Turn-Metadata`、`Version: 0.157.1` → 输出保留 `X-Codex-Turn-Metadata` 与 `Version`，无 `X-Forwarded-For`/`Cookie`，`Authorization` 为 CPA key。
  - Cherry Studio 请求带 `X-Stainless-Lang: js`、`User-Agent: OpenAI/JS 4.0` → 输出只含白名单头，UA = 渲染结果，不含 `X-Stainless-Lang`。
  - `ParseCodexUA("codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.154.0)")` 解析出 `Version=0.154.0, OS="Mac OS 26.5.2; arm64", Terminal="iTerm.app/3.6.11"`。
  - `SessionUUID("a") == SessionUUID("a")`，不同输入不同，匹配 UUID 正则。
  - Classifier：`codex_cli_rs/0.157.1 ...`、`codex-tui/...`、Originator `codex_vscode` → codex；`OpenAI/JS`、`Cursor/1.0` → other。
- [ ] 提交 `feat: client classification and upstream header shaping`

---

### Task 5: 响应解析（SSE / JSON）

**Files:** Create `internal/capture/sse.go`, `internal/capture/sse_test.go`, `internal/capture/testdata/*.sse|*.json`

**Interfaces:**
- Produces:

```go
type Usage struct{ Input, Cached, Output, Reasoning int64 }
type Outcome struct {
	Model string; Usage Usage; OutputItems []json.RawMessage // responses: output[]；chat: 一个组装后的 assistant message
	TTFT time.Duration; HasTTFT bool; ErrorType string; Completed bool
}
type StreamParser struct{...}
func NewStreamParser(endpoint string, start time.Time) *StreamParser // endpoint: "responses" | "chat"
func (p *StreamParser) Write(b []byte) (int, error) // 可作为 io.Writer 挂在 TeeReader 上，内部按行缓冲
func (p *StreamParser) Finish() Outcome
func ParseJSONResponse(endpoint string, body []byte) Outcome
```

解析规则：
- responses SSE：按 `event:`/`data:` 分帧。
  - 第一个 type 以 `.delta` 结尾的事件记为 TTFT。
  - `response.completed`、`response.incomplete`、`response.failed` 的 `data.response` 中取 `model`、`usage.input_tokens`、`usage.input_tokens_details.cached_tokens`、`usage.output_tokens`、`usage.output_tokens_details.reasoning_tokens`、`output`。
  - 若 `output` 为空，用累积的 `response.output_item.done` 事件的 `item` 作为输出。
  - `response.failed` 时 `ErrorType = response.error.code`。
- chat SSE：每个 `data:` JSON 块。
  - 第一个含非空 `choices[0].delta.content`、`reasoning_content` 或 `tool_calls` 的块记为 TTFT。
  - 拼接 content，按 index 合并 `tool_calls`（arguments 字符串拼接）。
  - `model` 取第一个非空值；`usage` 取最后一个非空值：`prompt_tokens`、`prompt_tokens_details.cached_tokens`、`completion_tokens`、`completion_tokens_details.reasoning_tokens`。
  - `[DONE]` 表示结束。
  - 输出组装为 `{"role":"assistant","content":...,"tool_calls":[...]}`。
- JSON：responses 取顶层字段，chat 取 `choices[0].message`。
- 行缓冲上限 8 MB；超长行丢弃，并记 `ErrorType="parse_overflow"`。

- [ ] 测试：分别用 fixture 验证用量、模型、输出条目数、TTFT > 0；把 fixture 按 1 字节、7 字节、整块分片写入，结果一致；截断流（无 completed）时 `Completed=false`。
- [ ] 提交 `feat: streaming response parser`

---

### Task 6: 片段拆分、脱敏、会话前缀编码、文本抽取

**Files:** Create `internal/capture/{segment,redact,encode,texts}.go` + 测试

**Interfaces:**
- Produces:

```go
type Segment struct{ Kind string; Data []byte /* 已脱敏、已 compact */; Hash [32]byte }
// SplitRequest 顺序：instructions?, tools?, 每个 input/messages 元素..., params（剩余字段）
// 返回 inputStart = 第一个 input 元素在列表中的下标
func SplitRequest(endpoint string, body []byte, r *Redactor) (segs []Segment, inputStart int, err error)
func SplitResponse(o Outcome, r *Redactor) []Segment

type Redactor struct{...}
func NewRedactor(rules []settings.RedactionRule) (*Redactor, error)
func (r *Redactor) Redact(b []byte) []byte   // 命中替换为 [REDACTED:<name>:<sha256(原文)前8位hex>]
func DefaultRedactionRules() []settings.RedactionRule

type Encoded struct{ PrefixOf *uuid.UUID; PrefixLen, ChainDepth int; Hashes []byte /* 32 字节拼接 */ }
const MaxChainDepth = 32
// Encode 做前缀压缩：prev 是同会话上一条请求的完整哈希列表、其 id 和链深。
// 若本次请求的哈希列表以 prev 为前缀，则只存指针(PrefixOf=prevID, PrefixLen=len(prev))
// 加尾部新增的哈希；否则整存(PrefixOf=nil)。链深达到 MaxChainDepth 时强制整存，避免还原链过长。
func Encode(hashes [][32]byte, prev *Encoded, prevID uuid.UUID) Encoded
// Decode 给定一条 requests 行的编码与"按 id 取前缀行"的回调，还原完整哈希列表。
func Decode(ctx context.Context, enc Encoded, fetch func(id uuid.UUID) (Encoded, error)) ([][32]byte, error)
```

- `texts.go`：`ExtractTexts(endpoint string, reqSegs []Segment, inputStart int, prevInputCount int, out Outcome) []TextRow`。
  - 只取本次请求中下标 ≥ `inputStart+prevInputCount` 的输入元素（即会话新增部分）的文本，`role=user`；
  - 取响应输出里的文本（`output_text` / assistant content），`role=assistant`；
  - 每条截断到 8000 字符。
- 默认脱敏规则（`DefaultRedactionRules`）：`openai-key ^sk-[A-Za-z0-9_-]{20,}`、`aws-akia AKIA[0-9A-Z]{16}`、`github-pat (ghp|github_pat)_[A-Za-z0-9_]{20,}`、`slack xox[abpr]-[A-Za-z0-9-]{10,}`、`private-key -----BEGIN [A-Z ]*PRIVATE KEY-----`、`jwt eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`、`url-cred [a-z]+://[^\s:@/]+:[^\s:@/]+@`。

- [ ] 测试：
  - `SplitRequest` 对 responses body 拆出 instructions/tools/input 段，`inputStart` 正确；相同 input 元素产生相同 hash。
  - `Redactor` 命中 `sk-...` 与私钥块，替换含固定指纹；不改变未命中的普通文本；对同一密钥两次脱敏得到相同占位符。
  - `Encode`：prev 为 `[a,b]`、本次 `[a,b,c]` → `PrefixOf=prevID,PrefixLen=2,Hashes=[c]`；本次 `[x]` → 整存。
  - `Encode/Decode` 往返：构造 3 级链，Decode 还原出完整列表。
  - 链深达到 32 时强制整存（`PrefixOf==nil`）。
  - `ExtractTexts` 只返回会话新增的用户输入。
- [ ] 提交 `feat: segmentation, redaction, prefix encoding, text extraction`

---

### Task 7: 异步记录器（落库 + 去重 + spool 回放）

**Files:** Create `internal/store/{requests,blobs,sessions,reports}.go`（补齐仓储）、`internal/capture/recorder.go` + 测试

**Interfaces:**
- Produces（`store`）：
  - `PutBlobs(ctx, segs []capture.Segment, refAt time.Time) error`（`ON CONFLICT (hash) DO UPDATE SET last_referenced_at=GREATEST(...)`，批量）
  - `GetBlob(ctx, hash [32]byte) (kind string, raw []byte, err error)`（解压）
  - `TouchBlobs(ctx, hashes [][32]byte, at time.Time)`
  - `UpsertSession(ctx, employeeID int64, accountID *int64, sessionKey, clientKind string, at time.Time) (sessionID int64, prevEncReq *capture.Encoded, prevReqID uuid.UUID, prevInputCount int, err error)`
  - `InsertRequest(ctx, r RequestRow) error`、`InsertTexts(ctx, []TextRow) error`
  - `GetRequest(ctx, id) (RequestRow, error)`、`ListRequests(ctx, RequestFilter) ([]RequestListItem, int, error)`
  - `PurgeExpiredBlobs(ctx, before time.Time) (int64, error)`、`MarkPurgedRequests(ctx, before time.Time) (int64, error)`
- Produces（`capture`）：

```go
type Record struct {
	RequestID uuid.UUID; StartedAt time.Time; EmployeeID int64; AccountID, APIKeyID *int64
	SessionKey, ClientKind, UserAgent, Endpoint string; Stream bool
	ModelRequested string; ReasoningEffort string
	StatusCode int; ErrorType, ErrorBody string; QueuedMS int
	ReqBody []byte; Outcome Outcome; DurationMS int
}
type Recorder struct{...}
func NewRecorder(st *store.Store, set *settings.Service, spoolDir string, log *slog.Logger) *Recorder
func (r *Recorder) Start(ctx context.Context)   // 起 worker + spool 回放
func (r *Recorder) Submit(rec Record)            // 非阻塞：入 channel，满则写 spool
func (r *Recorder) Flush(ctx context.Context) error // 测试用：处理完队列
```

处理流程（worker 内，单事务）：拆请求段+响应段 → 脱敏 → PutBlobs → UpsertSession 拿 prev → Encode 请求哈希 → InsertRequest → ExtractTexts + InsertTexts。DB 出错：整条 Record 以 JSON 追加到 `spoolDir/pending-*.jsonl`；`Start` 时先回放 spool。

- [ ] 测试（`testutil.NewDB`）：
  - 提交两条同会话、请求体后者在前者末尾追加一段的 Record → `blobs` 只多存 1 段（去重）；第二条 `req_prefix_of` 指向第一条。
  - `GetRequest` + 还原能重建两条完整请求体。
  - `request_texts` 里第二条只含新增的用户文本。
  - 关闭 DB 后 `Submit` → spool 文件出现该记录；重开 DB 调 `Start` 后记录落库、spool 清空。
  - `PurgeExpiredBlobs`：插入 old/new 两批，删除 old 后仍能还原 new 引用的请求。
- [ ] 提交 `feat: async recorder with dedup and spool replay`

---

### Task 8: CPA 客户端与账号接入

**Files:** Create `internal/cpa/{client,sync,onboard}.go` + 测试（用 `httptest` 假 CPA）

**Interfaces:**
- Produces（`cpa`，所有请求带 `Authorization: Bearer <ManagementKey>`；错误含响应体）：

```go
type Client struct{...}
func NewClient(baseURL, managementKey string, hc *http.Client) *Client
type AuthFile struct{ Name, Type, Email, Provider, Status string }
func (c *Client) ListAuthFiles(ctx) ([]AuthFile, error)         // GET /v0/management/auth-files -> {files:[{name,type,email,provider,status}]}
func (c *Client) UploadAuthFile(ctx, name string, content []byte) error // POST /v0/management/auth-files
func (c *Client) DeleteAuthFile(ctx, name string) error          // DELETE /v0/management/auth-files?name=
func (c *Client) PatchFields(ctx, name string, fields map[string]any) error // PATCH /auth-files/fields  {name, prefix, proxy_url}
func (c *Client) RequestCodexAuthURL(ctx) (url, state string, err error)     // GET /codex-auth-url -> {url,state}
func (c *Client) PostOAuthCallback(ctx, provider, redirectURL string) error   // POST /oauth-callback {provider,redirect_url}
func (c *Client) AuthStatus(ctx, state string) (status string, err error)     // GET /get-auth-status?state=
```

- Produces（`sync`）：`ReconcileAccount(ctx, st, box, acc store.Account) (drift string, err error)`：比对 CPA `auth-files` 中该 `cpa_auth_name` 的 prefix/proxy 是否等于期望值；缺失/不符时记录 drift 字符串并 `SetCPADrift`，**不**自动改（除非 `apply=true` 显式调用 `ApplyExpected`）。`ApplyExpected(ctx, client, box, acc)` 调 `PatchFields` 写入期望的 prefix 与解密后的 proxy_url。
- Produces（`onboard`）：
  - `StartOAuth(ctx, st, client, ipGroupID, os store.DeviceProfile, actorID) (authURL, state string, err error)`：记 `account_onboardings`（含当前 `known_auth_names` 快照）。
  - `CompleteOAuth(ctx, st, client, box, set, state, redirectURL string) (store.Account, error)`：`PostOAuthCallback` → 轮询 `AuthStatus` 直到 ok/超时 → `ListAuthFiles` 找出新增的 codex 文件 → 生成 `device_profile`（`InstallationID=uuid`，OS 取 onboarding 记录）→ `CreateAccount` → `ApplyExpected` 写 prefix+proxy。
  - `ImportAuthFile(ctx, st, client, box, set, ipGroupID int64, os store.DeviceProfile, content []byte) (store.Account, error)`：校验 JSON 含 `type:"codex"` 与 `refresh_token`/`access_token` → 生成随机 `cpa_auth_name` → `UploadAuthFile` → 建账号 → `ApplyExpected`。

- [ ] 测试（假 CPA）：
  - `ListAuthFiles` 解析 `{files:[...]}`。
  - `StartOAuth` 后 `CompleteOAuth`：假 CPA 在 callback 后 `auth-files` 多出一个 codex 文件，函数返回的账号 `CPAAuthName` 等于该文件名，`ApplyExpected` 发出的 PATCH body 含 `prefix=acct-<id>`、`proxy_url=<明文>`。
  - `ReconcileAccount`：CPA 中 prefix 不符 → 返回非空 drift 且不发 PATCH。
  - `ImportAuthFile` 拒绝非 codex JSON。
- [ ] 提交 `feat: CPA management client, sync and account onboarding`

---

### Task 9: 数据面 /v1 处理器

**Files:** Create `internal/proxy/{principal,handler}.go` + 测试（假 CPA 上游 + `testutil.NewDB`）

**Interfaces:**
- Consumes: `store`, `settings`, `Limiter`, `Classifier`, `BuildUpstreamHeaders`, `NewStreamParser`, `Recorder`, `cpa` base URL
- Produces:

```go
type Handler struct{...}
func NewHandler(deps HandlerDeps) *Handler
func (h *Handler) Routes(r chi.Router)   // POST /v1/responses, /v1/responses/compact, /v1/chat/completions; GET /v1/models
type PrincipalCache struct{...}          // key hash -> KeyPrincipal，TTL 10s，命中后异步 TouchKey
```

请求流程：
1. 取 `Authorization: Bearer`，`HashAPIKey`，`PrincipalCache` 查 `KeyPrincipal`。无效→401；员工 disabled→403；`!NoticeAcked`→403（`code:"notice_required"`）；`Account==nil`→403(`no_account_bound`)；账号 disabled→403；`NeedsLogin`→503(`account_needs_login`)；`RateLimitedUntil>now`→429 带 `Retry-After`。
2. `Classify` 客户端；记 `observed_clients`（仅 codex）。
3. 解析 body 取 `model`（去掉可能已有的前缀）、`stream`、`reasoning_effort`；WebSocket 升级→426。
4. `Limiter.Acquire(ctx, accountID, cap, timeout)`；`ErrQueueTimeout`→429(+`Retry-After:1`)；ctx 取消→499(不记为错误)。
5. `SessionUUID(seed)`：codex 用客户端 `Session-Id` 或 `prompt_cache_key`；否则 `employeeID + "|" + 首条 user 文本 hash`。sessionKey 同 seed。
6. 改写 body：`model = "acct-<id>/" + model`（用 sjson）。`BuildUpstreamHeaders`。
7. POST 到 `CPABaseURL + endpoint`，`ctx` 绑定客户端连接。设 upstream 超时（无流 100s，流式 600s）。
8. 流式：`io.Copy(flushWriter, TeeReader(resp.Body, parser))`，逐块 flush；非流式：读全 body 转发 + `ParseJSONResponse`。
9. release 名额（`defer`）。
10. 组装 `capture.Record`，`Recorder.Submit`。
11. 上游 429→`SetRateLimitedUntil`（解析 `X-Codex-*-Reset-At` 或 `Retry-After`）+ 记 `rate_limited` 信号；401/403→`SetNeedsLogin(true)` + 信号。
12. `model_requested != Outcome.Model`（都非空且去前缀后不同）→记 `model_mismatch` 信号。

`GET /v1/models`：向 CPA 请求 `/v1/models`，过滤/去掉 `acct-<id>/` 前缀，只返回该账号模型；失败则返回内置静态列表。

- [ ] 测试：
  - 假 CPA 记录收到的 header 与 model；断言 model 带 `acct-<id>/` 前缀，`Authorization` 为 CPA key，无 `X-Forwarded-For`。
  - 20 并发打同一账号（容量 4），假 CPA 用原子计数器观测到的峰值并发 = 4。
  - 无效 key→401 且 body 为 OpenAI 错误格式；未 ack→403 `notice_required`。
  - 假 CPA 返回 429（带 `X-Codex-Primary-Reset-At`）→ 账号 `RateLimitedUntil` 被设置，且新请求立即 429。
  - 流式 fixture 透传给客户端字节完全一致；`Recorder` 收到的 Outcome 用量正确。
  - 请求 model=`gpt-5.5`、响应 model=`gpt-5.6-luna` → 产生 `model_mismatch` 信号。
- [ ] 提交 `feat: /v1 data-plane handler`

---

### Task 10: 健康监测（信号、额度、出口 IP、Codex 版本）

**Files:** Create `internal/health/{signals,egress,codexversion}.go` + 测试

**Interfaces:**
- Produces:
  - `signals`：`EvaluateAccount(ctx, st, set, accountID) (Status, error)`（综合 needs_login/rate_limited/open signals 计算 spec §5.5 的状态）；`RecordQuotaFromHeaders(ctx, st, accountID, h http.Header) error`（解析 `X-Codex-Primary-Used-Percent` 等写 `quota_snapshots`）；`CheckDegradation(ctx, st, set, accountID) error`（近 24h `model_mismatch`≥阈值，或近 20 条 medium+ 推理中 ≥80% 相同且 ≤600 → 记/维持 `degraded` 信号，否则 resolve）。
  - `egress`：`CheckGroup(ctx, box, grp store.IPGroup, echoURL string) (ip string, err error)`（经该组代理 GET ip 回显）；`CheckAll(ctx, st, box, echoURL)`（逐组，记 `RecordEgressCheck`，变更时加 `egress_changed` 信号）。
  - `codexversion`：`Latest(ctx, hc) (version string, err error)`（`GET https://api.github.com/repos/openai/codex/releases/latest` 取 `tag_name` 去掉 `rust-v`）；`SuggestFromObserved(ctx, st) (ua, version string)`（近 7 天最高频 codex UA）。

- [ ] 测试：
  - `RecordQuotaFromHeaders` 解析 primary/secondary used_percent + reset_at 写库。
  - `CheckDegradation`：构造 4 条 model_mismatch → `degraded` 信号存在；清零后 resolve。
  - `EvaluateAccount` 各状态优先级正确（needs_login > rate_limited > degraded > active）。
  - `egress.CheckGroup` 用假回显服务器返回 IP；变更时 `CheckAll` 加信号。
  - `codexversion.Latest` 解析假 GitHub 响应。
- [ ] 提交 `feat: account health, quota, egress and codex version checks`

---

### Task 11: 飞书集成（登录、通讯录同步、消息）

**Files:** Create `internal/feishu/{client,sync}.go`, `internal/notify/notify.go` + 测试（假飞书 API）

**Interfaces:**
- Produces（`feishu`）：
  - `Client`：`TenantAccessToken(ctx)`（缓存至过期前 5 分钟）、`OAuthURL(state, redirectURI) string`、`ExchangeUser(ctx, code, redirectURI) (UserInfo, error)`、`ListDepartments(ctx) ([]Dept, error)`、`ListUsers(ctx) ([]User, error)`、`SendCard(ctx, openID string, card any) error`、`SendText(ctx, openID, text string) error`。
  - `sync.Run(ctx, st, client) error`：upsert departments（含 leader_open_ids）、users（upsert employee，绑定部门），并 `DisableEmployeesNotIn` 已离职者；写审计 `sync`。
- Produces（`notify`）：`Notifier.AccountAlert(ctx, acc, kind, detail)`：向超管发飞书文本卡片，`account_signals.notified_at` 去重，同一 (account,kind) 24h 内不重复。

- [ ] 测试（假飞书）：
  - `TenantAccessToken` 缓存命中不二次请求。
  - `ExchangeUser` 解析 open_id/name。
  - `sync.Run` upsert 后 employees/departments/关联表行数正确；离职者被 disable。
  - `Notifier` 对同一信号 24h 内只发一次。
- [ ] 提交 `feat: feishu client, directory sync and alerts`

---

### Task 12: Web 会话、RBAC 与管理/自助 API

**Files:** Create `internal/web/{session,rbac,router,api_auth,api_me,api_admin,api_logs,api_settings}.go` + 测试

**Interfaces:**
- Produces:
  - `session`：Cookie（httpOnly、SameSite=Lax、Secure）签发/校验；`RequireAuth` 中间件注入 `*store.Employee`；写操作校验 `X-CSRF-Token`。
  - `rbac`：`Scope(ctx, st, viewer) (Scope, error)`；`Scope.CanViewEmployee(id) bool`、`Scope.VisibleEmployeeIDs()`、`Scope.IsSuperAdmin`；超管全可见；部门负责人可见本部门及子部门员工（用 departments 树展开）；普通员工仅自己。
  - `router.Mount(r chi.Router, deps)`：
    - `GET /api/auth/feishu/login` → 重定向飞书；`GET /api/auth/feishu/callback` → 建会话；`POST /api/auth/logout`；`DEV_LOGIN`：`POST /api/auth/dev {open_id}`。
    - `GET /api/me`（含 role、notice 状态、绑定账号邮箱/状态）；`POST /api/me/ack-notice`；`POST /api/me/key`（签发，返回明文一次）；`GET /api/me/usage?range=`；`GET /api/me/quota`；`GET /api/me/reports`。
    - `GET /api/me/config-snippets`（Codex toml + 第三方说明，含 PublicBaseURL）。
    - Admin（`RequireSuperAdmin`）：`GET/POST /api/admin/ip-groups`、`GET /api/admin/accounts`、`POST /api/admin/accounts/oauth-start`、`POST /api/admin/accounts/oauth-complete`、`POST /api/admin/accounts/import`、`POST /api/admin/accounts/{id}/bind`、`/unbind`、`PATCH /api/admin/accounts/{id}`、`GET /api/admin/employees`、`GET /api/admin/overview`、`GET/PUT /api/admin/settings`、`POST /api/admin/sync-feishu`、`GET /api/admin/audit`、`GET /api/admin/codex-version`。
    - Logs（scope 内）：`GET /api/logs?employee_id=&from=&to=&q=&client=&model=`（列表）、`GET /api/logs/{id}`（还原完整对话，写 `view_transcript` 审计）。
    - 每个管理写操作调 `store.Audit`。

- [ ] 测试：
  - 未登录访问 `/api/me`→401；无 CSRF 的 POST→403。
  - `rbac.Scope`：部门负责人可见子部门员工、不可见他部门；普通员工仅自己。
  - `GET /api/logs/{id}` 越权（部门外）→403；本部门→200 且写审计。
  - `POST /api/me/key` 返回明文一次，重复调用撤销旧 key。
  - 非超管访问 admin→403。
- [ ] 提交 `feat: web sessions, RBAC and management/self-service API`

---

### Task 13: 前端 SPA

**Files:** Create `web/`（Vite+React+TS+Tailwind+TanStack Query+Recharts+react-router），`internal/webui/embed.go`；`vite.config.ts` 的 `build.outDir = "../internal/webui/dist"`。

**Interfaces:**
- Consumes: Task 12 的 `/api/*`
- Produces:
  - 员工端：登录页、首次告知确认、我的 Key + 一键配置（Codex toml / 第三方，可复制）、我的用量（Recharts 折线+按模型/客户端）、账号额度与状态、我的日报列表。
  - 管理端：总览、员工表、账号表 + 添加向导（选 IP 组→显示 OAuth 链接→粘贴回调 URL→完成 / 导入 JSON）、IP 组、日志检索 + 对话还原视图、日报、审计、设置表单。
  - 部门负责人：同管理端只读子集（隐藏写操作与全局设置）。
  - `internal/webui/embed.go`：`//go:embed dist`，`Handler() http.Handler`（SPA fallback 到 index.html；dist 缺失时返回占位页，保证后端可独立编译）。

- [ ] 步骤：`pnpm create vite` 脚手架 → 配置 Tailwind、路由、Query client（带 CSRF 头）→ 逐页实现 → `pnpm build` 产出 dist → `go build ./...` 通过（embed 生效）。
- [ ] 验证：`go vet ./...`；手动 `serve` 起服务，`DEV_LOGIN` 登录后各页渲染（用 Browser pane 截图核对）。
- [ ] 提交 `feat: web SPA (employee + admin)`

---

### Task 14: AI 日报

**Files:** Create `internal/report/{collect,llm,daily}.go` + 测试

**Interfaces:**
- Produces:
  - `collect.Material(ctx, st, employeeID, date, loc) (Material, error)`：取当日会话的 user/assistant 文本（工具输出截断 500 字、代码块 40 行）+ SQL 统计（请求数、token、活跃时段直方图、模型/客户端分布）。
  - `llm.Client`（OpenAI 兼容 `/chat/completions`）：`Summarize(ctx, prompt) (string, error)`。
  - `daily.GenerateForDate(ctx, deps, date) error`：对当日有活动员工生成会话小结→汇总日报（数字由 SQL 填模板，模型只写文字）→存 `daily_reports`→飞书推送（员工个人卡片；部门负责人本部门汇总卡片，无活动者列"无使用"）。
  - `daily.Deps` 含 `ReportEnabled`、LLM 配置（**独立 OpenAI key，非员工订阅**）。

- [ ] 测试：
  - `collect.Material` 只含当日、按会话聚合、统计数正确。
  - `daily.GenerateForDate` 用假 LLM（回显固定文本）+ 假飞书：生成 `daily_reports` 行，`stats` 中数字来自 DB 而非 LLM，`pushed_at` 被设置。
  - `ReportEnabled=false` 时不调用 LLM、不推送。
- [ ] 提交 `feat: AI daily report generation and push`

---

### Task 15: 定时任务、清理与 serve 装配

**Files:** Create `internal/jobs/{jobs,retention}.go`，补全 `cmd/gateway/main.go` 的 `serve` 与 `admin` 子命令

**Interfaces:**
- Produces:
  - `jobs.Register(cron, deps)`：Codex 版本检查（每日）、出口 IP 检查（每 30 分钟）、降智评估（每 15 分钟遍历账号）、未通知信号推送（每 5 分钟）、飞书同步（每小时）、日报（每日 08:00 Asia/Shanghai 生成前一日、09:00 推送）、保留清理（每日 03:30：`PurgeExpiredBlobs`+`MarkPurgedRequests`+删过期 `request_texts`+清理过期 `web_sessions`/`oauth_states`）。
  - `retention.Run(ctx, st, retentionDays, loc)`。
  - `serve`：`config.Load`→`db.Open`+`Migrate`→构造 store/settings/limiter/recorder/cpa/feishu/notifier→装配 chi（`/v1`、`/api`、SPA、`GET /healthz`）→启动 recorder worker、cron→优雅关闭。
  - `admin` 子命令：`bootstrap`（把 `BOOTSTRAP_SUPER_ADMINS` 里的 open_id upsert 为超管；首个飞书同步前用于建种子）、`issue-key <employee_id>`、`import-account --ip-group --os --file`。

- [ ] 测试：`retention.Run` 端到端删除过期原文、保留元数据；`jobs.Register` 注册的 cron 表达式可解析（`cron.Parse` 无 err）。
- [ ] 手动：`serve` 启动，`/healthz` 200。
- [ ] 提交 `feat: scheduled jobs, retention and serve/admin wiring`

---

### Task 16: 部署编排与文档

**Files:** Create `deploy/docker-compose.yml`, `deploy/Caddyfile`, `deploy/cpa/config.yaml.tmpl`, `deploy/.env.example`, `deploy/init.sh`, `Dockerfile`；`docs/`：`deploy.md`、`operations.md`（备份/恢复/换 IP/加账号）、`employee-guide.md`、`compliance-note.md`（告知与制度建议）；`README.md`。

**Interfaces / 内容:**
- `Dockerfile`：多阶段（Node 构建前端 → Go 构建 `gateway`），最终 `gcr.io/distroless/base` 或 alpine。
- `docker-compose.yml`：`caddy`、`gateway`、`cpa`（`image: eceasy/cli-proxy-api:<pinned>`，仅内网 network，挂 `./cpa/config.yaml`、`./cpa/auths`）、`postgres:16-alpine`（挂卷）。gateway 与 cpa 同一内网，cpa 不发布端口。
- `Caddyfile`：对外 443，反代 gateway；自动 TLS。
- `config.yaml.tmpl`：`force-model-prefix: true`、`disable-codex-cloaking: true`、`remote-management.secret-key`、`api-keys:[内部key]`、`passthrough-headers: true`、`request-log:false`。
- `.env.example`：所有必需环境变量（含 `MASTER_KEY` 生成说明）。
- `init.sh`：生成 MASTER_KEY / 内部 key / 管理 key，渲染 config，`docker compose up -d`，提示 `admin bootstrap`。

- [ ] 验证：`docker compose config` 校验通过；`docs/deploy.md` 步骤自洽；README 指向 spec/plan。
- [ ] 提交 `feat: deployment compose, Caddy, CPA config and docs`

---

### Task 17: 端到端验证（真实 CPA + 模拟上游）

**Files:** Create `test/e2e/e2e_test.go`（`//go:build e2e`）、`docs/verification/README.md`

**Interfaces / 内容:**
- 用源码构建的 CPA 二进制（复用 scratch 中已 clone 的仓库）指向一个本地"模拟 ChatGPT 上游"（httptest，套 codex responses SSE fixture），验证 spec §11 中可离线验证的假设：
  1. `force-model-prefix` + 前缀路由：请求 `acct-1/gpt-5.5` 命中账号 1；账号 1 `disabled` 时请求失败而非改走账号 2。
  2. `disable-codex-cloaking:true` 时 CPA 向（模拟）上游透传网关设置的 UA/Originator。
  3. `passthrough-headers:true` 时上游的 `X-Codex-Primary-Used-Percent` 回传到网关响应。
  4. chat completions 流式最后一块是否带 usage（据此决定是否回填）。
- 需联网/真实账号的假设（OAuth 登录流程、真实额度头、token 刷新走 proxy、Codex WebSocket 关闭项名）列入 `docs/verification/README.md` 作为上线前人工清单，附命令。

- [ ] 运行 `go test -tags e2e ./test/e2e/` 通过（CPA 不可构建时 `t.Skip` 并在 verification 文档说明）。
- [ ] 提交 `test: e2e verification against real CPA + mock upstream`

---

## Self-Review

**Spec coverage：** 需求1(绑定)=Task2/9；需求2(日志)=Task5/6/7/12；需求3(并发)=Task3/9；需求4(管控)=Task8/12；需求5(指纹)=Task4/9/16。M1=Task1–9；M2=Task11/12/13；M3=Task10/12/13；M4=Task14/15。§11 验证=Task17。合规告知=Task12(ack)+Task16(compliance-note)。

**Type consistency：** `cpa_auth_name` 全程一致；`Account.Prefix()` 唯一来源；`Outcome`/`Usage`/`Segment`/`Encoded`/`Record` 跨 Task5-9 签名一致；`KeyPrincipal` 定义于 Task2、消费于 Task9。

**已知偏差** 见顶部"与 Spec 的有意偏差"。

## Execution Handoff

计划已保存。采用 **Subagent-Driven**：每个 Task 派一个 fresh subagent 实现，我在 Task 之间复核并跑测试。