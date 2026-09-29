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
  key_prefix?: string;
  key_created_at?: string;
  account?: {
    email: string;
    plan_type: string;
    status: AccountStatus;
  };
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
  tokens_total: number;
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
  status: AccountStatus;
  rate_limited_until?: string | null;
  quota?: QuotaSnapshot | null;
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
  codex_toml: string;
  codex_env: string;
  third_party: {
    base_url: string;
    note: string;
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
  account_id: number | null;
  account_email: string;
  account_provider: string;
  account_status: string;
  active_key_prefix: string;
  department_names: string[] | null;
  frozen: boolean;
  frozen_reason: string;
  open_risk_events: number;
  remote_allowed: boolean;
  super_admin_pinned: boolean;
}

// --- accounts (snake_case) ---
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
  employee_id: number | null;
  employee_name: string;
  ip_group_id: number;
  ip_group_name: string;
  status: AccountStatus;
  needs_login: boolean;
  rate_limited_until: string | null;
  max_concurrency: number | null;
  device_profile: DeviceProfile;
  cpa_drift: string;
  quota?: AccountQuota; // latest snapshot; absent until first seen
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
  require_codex_installation_id: boolean;
  // Office networks (CIDRs or single IPs).
  office_only: boolean;
  office_networks: string[] | null;
  // Content audit of the newest user message.
  content_audit_mode: ContentAuditMode;
  content_audit_rules: ContentAuditRule[] | null;
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
  account_status?: AccountStatus;
  rate_limited_until?: string | null;
}
export interface TeamUsageResp {
  summary: UsageSummary;
  members: TeamMember[] | null; // sorted by name
  with_account: number;
}
