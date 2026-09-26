// Locale-aware formatting helpers (zh-CN).

const numberFmt = new Intl.NumberFormat("zh-CN");

export function fmtNum(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(n)) return "0";
  return numberFmt.format(n);
}

export function fmtDateTime(v: string | number | Date | null | undefined): string {
  if (!v) return "-";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function fmtDate(v: string | number | Date | null | undefined): string {
  if (!v) return "-";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
}

export function fmtDayShort(v: string | number | Date | null | undefined): string {
  if (!v) return "-";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}

export function fmtPct(v: number | null | undefined): string {
  if (v === null || v === undefined || Number.isNaN(v)) return "-";
  return `${v.toFixed(1)}%`;
}

export function fmtDuration(ms: number | null | undefined): string {
  if (!ms && ms !== 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}
