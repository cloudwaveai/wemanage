import type { AccountSubscription } from "./types";
import { fmtDateTime, fmtDayShort } from "./format";

// How a ChatGPT account's subscription reads: the next renewal, a cancelled
// subscription's end (amber within a week), or overdue payment.
export interface SubscriptionView {
  main: string; // "10/29 续费"
  sub: string; // "还有 21 天"
  tone: "normal" | "warn" | "bad";
  title: string; // details for a tooltip
  unverified: boolean; // the last check failed; dates are the last known
}

export function daysLeft(v: string): number {
  return Math.ceil((new Date(v).getTime() - Date.now()) / 86400000);
}

function left(v: string): string {
  const d = daysLeft(v);
  return d > 0 ? `还有 ${d} 天` : d === 0 ? "今天" : `已过 ${-d} 天`;
}

export function subscriptionView(s: AccountSubscription): SubscriptionView {
  const title = [
    s.plan && `套餐：${s.plan}`,
    s.renews_at && `下次续费：${fmtDateTime(s.renews_at)}`,
    s.expires_at && `本期到：${fmtDateTime(s.expires_at)}`,
    s.cancels_at && `已取消，${fmtDateTime(s.cancels_at)} 结束`,
    `查询于 ${fmtDateTime(s.checked_at)}`,
    s.error && `最近一次查询失败：${s.error}`,
  ]
    .filter(Boolean)
    .join("\n");
  const v = { title, unverified: !!s.error };
  if (s.delinquent) return { ...v, main: "欠费", sub: "扣款失败，尽快处理", tone: "bad" };
  if (s.cancels_at) {
    return {
      ...v,
      main: `${fmtDayShort(s.cancels_at)} 到期`,
      sub: `不再续费 · ${left(s.cancels_at)}`,
      tone: daysLeft(s.cancels_at) <= 7 ? "warn" : "normal",
    };
  }
  if (s.active && s.renews_at) return { ...v, main: `${fmtDayShort(s.renews_at)} 续费`, sub: left(s.renews_at), tone: "normal" };
  if (s.active && s.expires_at) return { ...v, main: `${fmtDayShort(s.expires_at)} 到期`, sub: left(s.expires_at), tone: "normal" };
  return { ...v, main: "未订阅", sub: s.expires_at ? `${fmtDayShort(s.expires_at)} 已结束` : "", tone: "bad" };
}

export const toneText: Record<SubscriptionView["tone"], string> = {
  normal: "text-slate-800",
  warn: "text-amber-600",
  bad: "text-rose-600",
};
