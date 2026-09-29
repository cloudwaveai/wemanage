// Locale-aware formatting helpers (zh-CN).

const numberFmt = new Intl.NumberFormat("zh-CN");

export function fmtNum(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(n)) return "0";
  return numberFmt.format(n);
}

const compactFmt = new Intl.NumberFormat("zh-CN", {
  notation: "compact",
  maximumFractionDigits: 1,
});

// Short form for big counts in tight table cells: 12345 → "1.2万".
export function fmtCompact(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(n)) return "0";
  return compactFmt.format(n);
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

// "刚刚", "5 分钟前", "3 小时前", "2 天前".
export function fmtAgo(v: string | number | Date | null | undefined): string {
  if (!v) return "-";
  const ms = Date.now() - new Date(v).getTime();
  if (Number.isNaN(ms)) return "-";
  const min = Math.floor(ms / 60_000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} 小时前`;
  return `${Math.floor(h / 24)} 天前`;
}

// A future time the way people say it: "15:20", "明天 09:10", "10月3日 09:10".
export function fmtWhen(v: string | number | Date | null | undefined): string {
  if (!v) return "-";
  const t = new Date(v);
  if (Number.isNaN(t.getTime())) return "-";
  const hm = t.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false });
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diff = Math.round((day(t) - day(new Date())) / 86_400_000);
  if (diff === 0) return hm;
  if (diff === 1) return `明天 ${hm}`;
  return `${t.getMonth() + 1}月${t.getDate()}日 ${hm}`;
}
