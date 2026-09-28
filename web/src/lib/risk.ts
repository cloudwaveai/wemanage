// Shared labels for the anti key-sharing (devices / networks / risk) feature.

import type {
  DeviceKind,
  DeviceStatus,
  Network,
  RiskMode,
  RiskSeverity,
} from "./types";

export const RULE_LABELS: Record<string, string> = {
  new_device: "新设备登录",
  non_codex_blocked: "非 Codex 客户端被拦截",
  datacenter_ip: "来自机房/云服务器 IP",
  too_many_devices: "设备数量过多",
  concurrent_untrusted: "未确认设备异地同时使用",
  multi_network: "多个网络同时使用",
  device_denied: "员工报告「不是我」",
  volume_spike: "用量暴增",
  sleepless: "全天候使用",
  outside_office: "公司网络外使用",
  content_flagged: "触发内容审核",
};

export function ruleLabel(rule: string): string {
  return RULE_LABELS[rule] ?? rule;
}

export const DEVICE_STATUS_META: Record<
  DeviceStatus,
  { label: string; cls: string }
> = {
  pending: { label: "待确认", cls: "bg-amber-100 text-amber-700" },
  trusted: { label: "已确认", cls: "bg-emerald-100 text-emerald-700" },
  blocked: { label: "已拉黑", cls: "bg-rose-100 text-rose-700" },
  expired: { label: "已过期（需确认）", cls: "bg-orange-100 text-orange-700" },
};

export const DEVICE_KIND_LABELS: Record<DeviceKind, string> = {
  codex_install: "Codex 安装",
  ua: "按客户端标识识别",
};

export function deviceKindLabel(kind: string): string {
  return DEVICE_KIND_LABELS[kind as DeviceKind] ?? kind;
}

export const RISK_MODE_LABELS: Record<RiskMode, string> = {
  lax: "宽松（只通知）",
  standard: "标准（推荐）",
  strict: "严格（新设备先确认才能用）",
};

export const RISK_MODES: RiskMode[] = ["lax", "standard", "strict"];

export const SEVERITY_META: Record<
  RiskSeverity,
  { label: string; cls: string }
> = {
  high: { label: "高", cls: "bg-rose-100 text-rose-700" },
  warn: { label: "警告", cls: "bg-amber-100 text-amber-700" },
  info: { label: "提示", cls: "bg-slate-100 text-slate-600" },
};

// Friendly names for keys that appear in RiskEvent.detail.
export const DETAIL_KEY_LABELS: Record<string, string> = {
  ip: "IP",
  last_ip: "最近 IP",
  location: "位置",
  isp: "运营商",
  label: "设备",
  user_agent: "UA",
  client_kind: "客户端",
  active_devices: "活跃设备数",
  limit: "上限",
  requests: "请求数",
  day: "日期",
  active_hours: "活跃小时数",
  median_14d: "14 天中位数",
  networks: "网络",
  network: "网络",
  other_network: "另一网络",
  device_id: "设备 ID",
  other_device_id: "另一设备 ID",
  rule: "规则",
  snippet: "内容片段",
  blocked: "已拦截",
};

function detailValue(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "boolean") return v ? "是" : "否";
  if (Array.isArray(v)) return v.map(detailValue).join("、");
  if (typeof v === "object") {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}

// Compact one-line rendering of a risk event's detail object.
export function detailText(d: Record<string, unknown> | null | undefined): string {
  if (!d) return "";
  return Object.entries(d)
    .map(([k, v]) => `${DETAIL_KEY_LABELS[k] ?? k}：${detailValue(v)}`)
    .join(" · ");
}

// "中国 广东 深圳" with consecutive duplicates (e.g. 北京 北京) removed.
export function networkLocation(n: Pick<Network, "country" | "province" | "city">): string {
  const parts: string[] = [];
  for (const p of [n.country, n.province, n.city]) {
    const s = (p ?? "").trim();
    if (s && parts[parts.length - 1] !== s) parts.push(s);
  }
  return parts.join(" ");
}
