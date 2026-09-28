import React from "react";
import type { AccountStatus } from "../lib/types";
import { fmtDateTime } from "../lib/format";

// ---- Card ---------------------------------------------------------------
export function Card({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={
        "rounded-xl border border-slate-200 bg-white shadow-sm " + className
      }
    >
      {children}
    </div>
  );
}

export function PageHeader({
  title,
  desc,
  right,
}: {
  title: string;
  desc?: string;
  right?: React.ReactNode;
}) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold text-slate-900">{title}</h1>
        {desc && <p className="mt-1 text-sm text-slate-500">{desc}</p>}
      </div>
      {right && <div className="flex items-center gap-2">{right}</div>}
    </div>
  );
}

// ---- Stat tile ----------------------------------------------------------
export function StatTile({
  label,
  value,
  hint,
}: {
  label: string;
  value: React.ReactNode;
  hint?: string;
}) {
  return (
    <Card className="p-4">
      <div className="text-xs font-medium uppercase tracking-wide text-slate-400">
        {label}
      </div>
      <div className="mt-1.5 text-2xl font-semibold text-slate-900">{value}</div>
      {hint && <div className="mt-1 text-xs text-slate-400">{hint}</div>}
    </Card>
  );
}

// ---- Loading / Error / Empty -------------------------------------------
export function Loading({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center py-12 text-sm text-slate-400">
      <span className="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-indigo-500" />
      {label}
    </div>
  );
}

export function ErrorBox({ error }: { error: unknown }) {
  const msg =
    error instanceof Error ? error.message : String(error ?? "未知错误");
  return (
    <div className="rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
      加载失败：{msg}
    </div>
  );
}

export function Empty({ label = "暂无数据" }: { label?: string }) {
  return (
    <div className="py-12 text-center text-sm text-slate-400">{label}</div>
  );
}

// ---- Buttons ------------------------------------------------------------
type BtnProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
};
export function Button({
  variant = "secondary",
  className = "",
  children,
  ...rest
}: BtnProps) {
  const base =
    "inline-flex items-center justify-center gap-1.5 rounded-lg px-3.5 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50";
  const styles: Record<string, string> = {
    primary: "bg-indigo-600 text-white hover:bg-indigo-500",
    secondary:
      "border border-slate-300 bg-white text-slate-700 hover:bg-slate-50",
    danger: "bg-rose-600 text-white hover:bg-rose-500",
    ghost: "text-slate-600 hover:bg-slate-100",
  };
  return (
    <button className={`${base} ${styles[variant]} ${className}`} {...rest}>
      {children}
    </button>
  );
}

// ---- Switch -------------------------------------------------------------
export function Switch({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className="inline-flex items-center gap-2 text-sm text-slate-700 disabled:cursor-not-allowed disabled:opacity-50"
    >
      <span
        className={
          "relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors " +
          (checked ? "bg-indigo-600" : "bg-slate-300")
        }
      >
        <span
          className={
            "absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform " +
            (checked ? "translate-x-[18px]" : "translate-x-0.5")
          }
        />
      </span>
      {label}
    </button>
  );
}

// ---- Usage range tabs (今天 / 7 天 / 30 天) --------------------------------
const USAGE_RANGES = [
  { v: 1, label: "今天" },
  { v: 7, label: "7 天" },
  { v: 30, label: "30 天" },
];

export function RangeTabs({
  value,
  onChange,
}: {
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
      {USAGE_RANGES.map((r) => (
        <button
          key={r.v}
          onClick={() => onChange(r.v)}
          className={
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
            (value === r.v
              ? "bg-indigo-600 text-white"
              : "text-slate-600 hover:bg-slate-100")
          }
        >
          {r.label}
        </button>
      ))}
    </div>
  );
}

// ---- Status badge -------------------------------------------------------
const STATUS_META: Record<
  string,
  { label: string; cls: string }
> = {
  active: { label: "正常", cls: "bg-emerald-100 text-emerald-700" },
  rate_limited: { label: "限流", cls: "bg-amber-100 text-amber-700" },
  needs_login: { label: "需登录", cls: "bg-rose-100 text-rose-700" },
  degraded: { label: "降级", cls: "bg-purple-100 text-purple-700" },
  disabled: { label: "已停用", cls: "bg-slate-200 text-slate-600" },
};

export function StatusBadge({
  status,
  fallbackLabel,
}: {
  status: string;
  fallbackLabel?: string;
}) {
  const meta = STATUS_META[status];
  const label = meta?.label ?? fallbackLabel ?? status ?? "-";
  const cls = meta?.cls ?? "bg-slate-100 text-slate-600";
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}
    >
      {label}
    </span>
  );
}

export function accountStatusLabel(s: AccountStatus | string): string {
  return STATUS_META[s]?.label ?? s;
}

// ---- Simple horizontal bar list ----------------------------------------
export function BarList({
  items,
  emptyLabel = "暂无数据",
}: {
  items: { label: string; value: number; sub?: string }[];
  emptyLabel?: string;
}) {
  if (!items.length)
    return <div className="py-4 text-sm text-slate-400">{emptyLabel}</div>;
  const max = Math.max(...items.map((i) => i.value), 1);
  return (
    <div className="space-y-2">
      {items.map((it, idx) => (
        <div key={idx}>
          <div className="mb-0.5 flex items-center justify-between text-xs text-slate-600">
            <span className="truncate pr-2 font-medium">{it.label}</span>
            <span className="tabular-nums text-slate-500">
              {it.sub ?? it.value}
            </span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-slate-100">
            <div
              className="h-full rounded-full bg-indigo-500"
              style={{ width: `${(it.value / max) * 100}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

// ---- Percent meter ------------------------------------------------------
export function Meter({
  label,
  pct,
  hint,
}: {
  label: string;
  pct: number | null | undefined;
  hint?: string;
}) {
  const v = pct ?? 0;
  const color =
    v >= 90 ? "bg-rose-500" : v >= 70 ? "bg-amber-500" : "bg-indigo-500";
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-xs text-slate-600">
        <span className="font-medium">{label}</span>
        <span className="tabular-nums">
          {pct === null || pct === undefined ? "-" : `${v.toFixed(1)}%`}
        </span>
      </div>
      <div className="h-2.5 w-full overflow-hidden rounded-full bg-slate-100">
        <div
          className={`h-full rounded-full ${color}`}
          style={{ width: `${Math.min(Math.max(v, 0), 100)}%` }}
        />
      </div>
      {hint && <div className="mt-1 text-xs text-slate-400">{hint}</div>}
    </div>
  );
}

// ---- Quota window cell -------------------------------------------------
// One quota window as a small meter; reset / snapshot times on hover.
export function QuotaCell({
  pct,
  resetAt,
  at,
}: {
  pct: number | null | undefined;
  resetAt: string | null | undefined;
  at: string | undefined;
}) {
  if (pct === null || pct === undefined)
    return <span className="text-slate-300">—</span>;
  // The snapshot predates the window reset, so the real usage is likely lower.
  const stale = !!resetAt && new Date(resetAt).getTime() < Date.now();
  const title = [
    resetAt ? `重置于 ${fmtDateTime(resetAt)}` : "",
    at ? `更新于 ${fmtDateTime(at)}` : "",
    stale ? "已过重置时间，实际用量可能已清零" : "",
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <div className={"w-[88px]" + (stale ? " opacity-50" : "")} title={title}>
      <Meter label="" pct={pct} />
    </div>
  );
}
