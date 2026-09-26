// API response types. snake_case unless noted as Go-cased (marshaled from Go
// structs without json tags) — those are called out explicitly.

export type Role = "employee" | "leader" | "super_admin";

export interface Me {
  id: number;
  name: string;
  email: string;
  avatar_url: string;
  role: Role;
  notice_acked: boolean;
  notice_text: string;
  csrf: string;
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
export interface DailyReport {
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
  notice_acked: boolean;
  account_id: number | null;
  account_email: string;
  account_status: string;
  active_key_prefix: string;
  department_names: string[] | null;
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
  notice_text: string;
  default_device_profile: DeviceProfile;
}
