// API response types. snake_case unless noted as Go-cased (marshaled from Go
// structs without json tags) — those are called out explicitly.

export type Role = "employee" | "leader" | "super_admin";

export type RiskMode = "lax" | "standard" | "strict";

export interface Me {
  id: number;
  name: string;
  email: string;
  avatar_url: string;
  role: Role;
  csrf: string;
  frozen: boolean;
  frozen_reason: string;
  frozen_at: string | null;
  risk_mode: RiskMode;
  pending_devices: number;
  // Office-network restriction: when office_only is on and remote_allowed is
  // off, the key only works from the company's egress IPs.
  office_only: boolean;
  remote_allowed: boolean;
  weekly_report: boolean; // the employee receives their weekly recap
  quota_alert: boolean; // the employee is told in Feishu when quota reaches 80%
  key_prefix?: string;
  key_created_at?: string;
  // The employee's accounts, oldest binding first (several accounts are used
  // in turn: one that runs out hands over to the next).
  accounts: MyAccount[];
  // The employee's latest request that got through the gateway, absent until
  // one has; cli: version is the Codex CLI's (comparable to codex_version).
  last_success?: { at: string; client: string; version: string; cli: boolean };
  codex_version: string; // the Codex CLI release the gateway targets
}

export interface MyAccount {
  id: number;
  email: string;
  plan_type: string;
  provider: string;
  status: AccountStatus;
}

export type AccountStatus =
  | "active"
  | "rate_limited"
  | "needs_login"
  | "degraded"
  | "disabled";

// --- usage (snake_case) ---
export interface UsagePoint {
  day: string;
  requests: number;
  tokens_input: number;
  tokens_output: number;
}
export interface UsageBreakdown {
  key: string;
  requests: number;
  tokens_total: number; // input + output
  tokens_input: number;
  tokens_cached: number; // part of input
  tokens_output: number;
  tokens_reasoning: number; // part of output
}
export interface UsageSummary {
  requests: number;
  tokens_input: number;
  tokens_cached: number;
  tokens_output: number;
  tokens_reasoning: number;
  by_model: UsageBreakdown[] | null;
  by_client: UsageBreakdown[] | null;
  daily: UsagePoint[] | null;
  // Time series at a resolution chosen from the data (5 min … 1 day).
  series?: UsageSeries;
}
export interface UsageSeries {
  bucket_seconds: number; // 0 when there is no data
  points: { t: string; requests: number; tokens: number }[];
}

// --- quota (wrapper snake_case; inner quota Go-cased) ---
export interface QuotaSnapshot {
  AccountID: number;
  At: string;
  PrimaryUsedPct: number | null;
  PrimaryWindowMinutes: number | null;
  PrimaryResetAt: string | null;
  SecondaryUsedPct: number | null;
  SecondaryWindowMinutes: number | null;
  SecondaryResetAt: string | null;
}
export interface QuotaResp {
  accounts: (MyAccount & {
    rate_limited_until?: string | null;
    quota?: QuotaSnapshot | null;
  })[];
}

// --- reports (Go-cased) ---
// A weekly AI-use recap, sent only to the employee. Date is the week's Monday.
export interface Report {
  ID: number;
  EmployeeID: number;
  Date: string;
  Stats: unknown; // arbitrary JSON object
  SummaryMD: string;
  Model: string;
  GeneratedAt: string;
  PushedAt: string | null;
}

// --- config snippets ---
export interface ConfigSnippets {
  base_url: string;
  provider: string; // bound account's provider, "" when none
  // Models the bound account can use (newest first), or CPA's ChatGPT
  // catalogue when no account is bound yet (models_source "catalog").
  models: { id: string; name: string; created: number }[];
  models_source: "account" | "catalog";
  default_model: string;
  // config_toml: replacement for CC Switch's config (image generation + web
  // search); "" for accounts that cannot use them. Fill {{KEY}} and {{MODEL}}.
  ccswitch: { name: string; endpoint: string; homepage: string; config_toml: string };
  // Manual config.toml with {{KEY}} and {{MODEL}} to fill in.
  codex_toml: string;
  third_party: {
    base_url: string;
    allowed: boolean; // some account of the employee is open to other clients
    note: string;
    // Ready configs; the browser fills {{KEY}} and {{MODEL}}.
    opencode_json: string; // ~/.config/opencode/opencode.json
    commandcode_json: string; // ~/.commandcode/providers.json
    commandcode_env: string; // shell line exporting the key
    claude_code_sh: string; // ~/.zshrc alias "claude-gw"
    claude_code_ps: string; // PowerShell profile function "claude-gw"
  };
}

// --- logs (snake_case) ---
export interface LogRow {
  id: string;
  started_at: string;
  employee_id: number;
  employee_name: string;
  client_kind: string;
  endpoint: string;
  model_requested: string;
  model_responded: string;
  status_code: number;
  error_type: string;
  tokens_input: number;
  tokens_output: number;
  duration_ms: number;
}
export interface LogList {
  requests: LogRow[] | null;
  total: number;
}
export interface TranscriptSegment {
  kind: string;
  data: unknown;
}
export interface Transcript {
  request: TranscriptSegment[] | null;
  response: TranscriptSegment[] | null;
  purged: boolean;
}
export interface LogDetail {
  id: string;
  started_at: string;
  employee_id: number;
  client_kind: string;
  endpoint: string;
  model_requested: string;
  model_responded: string;
  status_code: number;
  tokens: { input: number; cached: number; output: number; reasoning: number };
  transcript: Transcript;
}

// --- admin overview ---
export interface Alert {
  account_id: number;
  account_email: string;
  kind: string;
  at: string;
}
export interface Overview {
  usage_30d: UsageSummary;
  active_24h: number;
  account_health: Record<string, number>;
  account_total: number;
  alerts: Alert[] | null;
}

// --- employees (snake_case) ---
export interface EmployeeRow {
  id: number;
  name: string;
  email: string;
  status: string;
  is_super_admin: boolean;
  accounts: { id: number; email: string; provider: string; plan_type: string; status: AccountStatus }[];
  active_key_prefix: string;
  department_names: string[] | null;
  frozen: boolean;
  frozen_reason: string;
  open_risk_events: number;
  remote_allowed: boolean;
  super_admin_pinned: boolean;
  is_service: boolean; // a platform caller, not a Feishu employee
}

// --- accounts (snake_case) ---
// A ChatGPT account's subscription as ChatGPT reports it (checked every 6 h).
export interface AccountSubscription {
  plan: string;
  active: boolean;
  renews_at: string | null; // next charge, while it renews
  expires_at: string | null; // end of the paid period
  cancels_at: string | null; // set when cancelled: it ends then, no renewal
  delinquent: boolean; // payment failed
  checked_at: string;
  error?: string; // last check failed; the dates above are the last known
}
export interface DeviceProfile {
  os: string;
  terminal: string;
  installation_id?: string;
}
export interface AccountRow {
  id: number;
  email: string;
  plan_type: string;
  owner_type: string;
  provider?: string; // "codex" (ChatGPT) | "kimi" | …
  members: { id: number; name: string }[]; // employees sharing the account
  ip_group_id: number;
  ip_group_name: string;
  status: AccountStatus;
  needs_login: boolean;
  rate_limited_until: string | null;
  max_concurrency: number | null;
  device_profile: DeviceProfile;
  cpa_drift: string;
  allowed_models: string[] | null; // null: every model
  allow_third_party: boolean; // clients other than Codex may use it
  quota?: AccountQuota; // latest snapshot; absent until first seen
  subscription?: AccountSubscription; // ChatGPT accounts, once checked
  usage: AccountUsage;
}
export interface AccountQuota {
  at: string;
  primary_used_pct: number | null; // 5-hour window, 0–100
  primary_window_minutes: number | null;
  primary_reset_at: string | null;
  secondary_used_pct: number | null; // weekly window, 0–100
  secondary_window_minutes: number | null;
  secondary_reset_at: string | null;
}
export interface AccountUsage {
  requests_24h: number;
  tokens_24h: number;
  requests_7d: number;
  tokens_7d: number;
  requests_all: number;
  tokens_all: number;
  last_active: string | null;
}

// --- ip groups (snake_case) ---
export interface IPGroupRow {
  id: number;
  name: string;
  capacity: number;
  account_count: number;
  expected_egress_ip: string;
  last_egress_ip: string;
  last_check_error: string;
  last_checked_at: string | null;
  proxy_masked: string;
}

// --- audit (Go-cased) ---
export interface AuditRow {
  ID: number;
  ActorID: number | null;
  ActorName: string;
  Action: string;
  TargetType: string;
  TargetID: string;
  Detail: Record<string, unknown> | null;
  IP: string;
  At: string;
}

// --- settings (snake_case) ---
export interface CodexProfile {
  version: string;
  ua_template: string;
  originator: string;
}
export interface ClientRule {
  kind: string;
  header: string;
  pattern: string;
}
export interface RedactionRule {
  name: string;
  pattern: string;
}
export type ContentAuditMode = "off" | "warn" | "block";
export interface ContentAuditRule {
  name: string;
  pattern: string; // case-insensitive regex
}
export interface Settings {
  default_max_concurrency: number;
  queue_timeout_seconds: number;
  retention_days: number;
  codex_profile: CodexProfile;
  client_rules: ClientRule[] | null;
  redaction_rules: RedactionRule[] | null;
  model_mismatch_threshold: number;
  report_enabled: boolean;
  report_llm_model: string;
  default_device_profile: DeviceProfile;
  // Risk controls (anti key-sharing).
  risk_mode: RiskMode;
  allow_non_codex: boolean;
  disable_websocket?: boolean;
  default_codex_model?: string;
  device_confirm_hours: number;
  concurrency_window_minutes: number;
  new_device_grace_minutes: number;
  max_active_devices: number;
  max_account_members: number; // how many employees may share one account
  require_codex_installation_id: boolean;
  // Office networks (CIDRs or single IPs).
  office_only: boolean;
  office_networks: string[] | null;
  // Content audit of the newest user message.
  content_audit_mode: ContentAuditMode;
  content_audit_rules: ContentAuditRule[] | null;
  // Work board summaries: the ChatGPT account (0 = off) and model ("" = default).
  work_digest_account_id?: number;
  work_digest_model?: string;
}

// --- devices / networks / risk (snake_case) ---
export type DeviceStatus = "pending" | "trusted" | "blocked" | "expired";
export type DeviceKind = "codex_install" | "ua";

export interface Device {
  id: number;
  employee_id: number;
  device_key: string;
  kind: DeviceKind;
  label: string;
  client_kind: string;
  user_agent: string;
  status: DeviceStatus;
  first_seen: string;
  last_seen: string;
  first_ip: string;
  last_ip: string;
  requests: number;
  confirmed_at: string | null;
  notified_at: string | null;
}

export interface Network {
  id: number;
  employee_id: number;
  net_key: string;
  ip_sample: string;
  // Empty strings when no GeoIP database is configured.
  country: string;
  province: string;
  city: string;
  isp: string;
  datacenter: boolean;
  first_seen: string;
  last_seen: string;
  requests: number;
}

export type RiskSeverity = "info" | "warn" | "high";

export interface RiskEvent {
  id: number;
  employee_id: number;
  employee_name: string;
  rule: string;
  severity: RiskSeverity;
  detail: Record<string, unknown> | null;
  device_id: number | null;
  network_id: number | null;
  at: string;
  notified_at: string | null;
  resolved_at: string | null;
}

export interface MyDevicesResp {
  devices: Device[];
  confirm_hours: number;
  risk_mode: RiskMode;
  max_trusted: number; // 0 = unlimited
}

export interface AdminEmployeeDevicesResp {
  employee: {
    id: number;
    name: string;
    email: string;
    frozen: boolean;
    frozen_at: string | null;
    frozen_reason: string;
    remote_allowed: boolean;
  };
  devices: Device[];
  networks: Network[];
  events: RiskEvent[];
}

// --- team (snake_case) ---
// Leaders see team totals and whether each member's account works; not
// per-person usage, activity times or conversations.
export interface TeamMember {
  employee_id: number;
  name: string;
  department_names: string[] | null;
  has_account: boolean;
  accounts?: number;
  account_status?: AccountStatus; // the best of the member's accounts
  rate_limited_until?: string | null;
}
export interface TeamUsageResp {
  summary: UsageSummary;
  members: TeamMember[] | null; // sorted by name
  with_account: number;
}

// The rate-limit reset credits (重置卡) of each of the employee's ChatGPT accounts.
export interface ResetCreditsResp {
  accounts: AccountResetCredits[];
}
export interface AccountResetCredits {
  account_id: number;
  email: string;
  plan_type: string;
  supported: boolean;
  available: number;
  credits: { id: string; title: string; status: string; expires_at: string | null }[];
  can_use: boolean;
  reason?: string; // why they cannot be used now
  error?: string; // they could not be looked up
}

// An account's models as an administrator sees them.
export interface AccountModelsResp {
  restricted: boolean; // false: every model may be used
  models: {
    id: string;
    name: string;
    created: number;
    allowed: boolean;
    offered: boolean; // CPA lists it for the account
    requests_7d: number;
  }[];
}

// --- account logins CPA offers (admin) ---
export interface LoginProvider {
  id: string; // CPA auth-file type: codex, kimi, antigravity, xai, ...
  label: string;
  note: string;
  paste: boolean; // redirect login: paste the final URL back
  callback?: string;
}

// --- work board (admin) ---
export type WorkRange = "today" | "yesterday" | "7d" | "30d";

export interface WorkEmployee {
  employee_id: number;
  name: string;
  department_names: string[];
  is_service: boolean;
  status: string;
  requests: number;
  tokens_input: number; // includes cached
  tokens_cached: number;
  tokens_output: number; // includes reasoning
  tokens_reasoning: number;
  tokens_total: number; // input + output
  sessions: number;
  active_days: number;
  last_active: string | null;
  top_model: string;
}

export interface Workboard {
  from: string;
  to: string;
  days: number;
  totals: {
    requests: number;
    tokens_input: number;
    tokens_cached: number;
    tokens_output: number;
    tokens_reasoning: number;
    tokens_total: number;
    active_employees: number;
  };
  employees: WorkEmployee[];
  idle: { employee_id: number; name: string }[];
  daily: { t: string; requests: number; tokens: number }[];
}

export type DigestStatus = "none" | "running" | "done" | "failed";

export interface WorkDigests {
  day: string; // YYYY-MM-DD
  status: DigestStatus;
  error?: string;
  configured: boolean;
  model?: string;
  account?: { id: number; email: string; plan_type: string };
  overview_md?: string;
  started_at?: string;
  finished_at?: string | null;
  days: string[]; // days that have summaries, newest first
  employees: {
    employee_id: number;
    name: string;
    department_names: string[];
    summary_md: string;
    sessions: number;
    messages: number;
    generated_at: string;
  }[];
}

export type BriefingStatus = "running" | "test" | "sent" | "failed";

export interface AdminBriefingRow {
  status: BriefingStatus;
  content_md?: string;
  error?: string;
  model?: string;
  generated_at?: string | null;
  sent_at?: string | null;
}

// Dates are inclusive company-local calendar dates.
export interface AdminBriefingConfig {
  configured: boolean;
  model?: string;
  account?: { id: number; email: string; plan_type: string };
  enabled: boolean;
  prompt: string;
  default_prompt: string;
  from: string;
  to: string;
  today: string;
  digest_days?: number;
  running: boolean;
  briefing?: AdminBriefingRow | null;
  history?: (AdminBriefingRow & { from: string; to: string })[];
}
