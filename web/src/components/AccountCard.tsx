import type { ReactNode } from "react";
import type { AccountRow } from "../lib/types";
import { StatusBadge } from "./ui";
import { fmtCompact, fmtDateTime, fmtNum } from "../lib/format";
import { accountName, hasQuota, isChatGPT, providerIcon, providerName, NO_FIVE_HOUR, weeklyOnly } from "../lib/providers";
import { subscriptionView, toneText } from "../lib/subscription";

// An account as a card, in the manner of CPA's management center: who it is
// (logo, name, plan, IP group, status), when it renews, its quota windows,
// who uses it and how much, and its actions.

export function AccountCard({
  a,
  maxMembers,
  onBind,
  onUnbind,
  onRefresh,
  refreshing,
  onModels,
  onEdit,
  onSubscription,
}: {
  a: AccountRow;
  maxMembers: number;
  onBind: () => void;
  onUnbind: (employeeID: number, name: string) => void;
  onRefresh: () => void;
  refreshing: boolean;
  onModels: () => void;
  onEdit: () => void;
  onSubscription: () => void;
}) {
  const icon = providerIcon(a.provider);
  const name = providerName(a.provider);
  return (
    <article className="flex flex-col rounded-2xl border border-slate-200 bg-white shadow-sm transition-shadow hover:shadow-md">
      <header className="flex items-start gap-3 px-4 pt-4">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-slate-50 ring-1 ring-slate-200">
          {icon ? (
            <img src={icon} alt={name} className="h-6 w-6" />
          ) : (
            <span className="text-sm font-semibold text-slate-500">{name.slice(0, 1)}</span>
          )}
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium text-slate-900" title={a.email || undefined}>
            {accountName(a)}
          </div>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-xs text-slate-400">
            <span>{name}</span>
            {a.plan_type && (
              <>
                <Dot />
                <span className="font-medium uppercase text-slate-500">{a.plan_type}</span>
              </>
            )}
            <Dot />
            <span title="出口 IP 组">{a.ip_group_name || "未分组"}</span>
            {a.owner_type === "employee" && (
              <>
                <Dot />
                <span>员工自有</span>
              </>
            )}
          </div>
        </div>
        <StatusBadge status={a.status} />
      </header>

      <div className="flex-1 space-y-3.5 px-4 py-4">
        {isChatGPT(a.provider) && <Subscription a={a} onOpen={onSubscription} />}

        {hasQuota(a.provider) ? (
          <div className="space-y-2.5">
            <QuotaMeter
              label="5 小时"
              pct={a.quota?.primary_used_pct}
              resetAt={a.quota?.primary_reset_at}
              at={a.quota?.at}
              emptyText={weeklyOnly(a.quota?.primary_used_pct, a.quota?.secondary_used_pct) ? NO_FIVE_HOUR : undefined}
            />
            <QuotaMeter label="每周" pct={a.quota?.secondary_used_pct} resetAt={a.quota?.secondary_reset_at} at={a.quota?.at} />
          </div>
        ) : (
          <p className="rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-400">这类账号没有 5 小时 / 每周额度显示</p>
        )}

        <Tags a={a} />

        <div>
          <div className="mb-1.5 text-xs text-slate-400">
            使用人 <span className="tabular-nums">{a.members.length}/{maxMembers}</span>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {a.members.map((m) => (
              <span
                key={m.id}
                className="inline-flex items-center gap-1 rounded-full bg-indigo-50 py-0.5 pl-2.5 pr-1 text-xs text-indigo-700"
              >
                {m.name}
                <button
                  onClick={() => onUnbind(m.id, m.name)}
                  className="rounded-full px-1 text-indigo-300 hover:bg-indigo-100 hover:text-indigo-600"
                  title="移除"
                  aria-label={`移除 ${m.name}`}
                >
                  ×
                </button>
              </span>
            ))}
            {a.members.length < maxMembers ? (
              <button
                onClick={onBind}
                className="rounded-full border border-dashed border-slate-300 px-2.5 py-0.5 text-xs text-slate-500 hover:border-indigo-300 hover:bg-indigo-50 hover:text-indigo-600"
              >
                + 添加
              </button>
            ) : (
              <span className="text-[11px] text-slate-400">已满</span>
            )}
          </div>
        </div>

        <div
          className="grid grid-cols-3 divide-x divide-slate-200 rounded-xl bg-slate-50 py-2 text-center"
          title={`请求：24h ${fmtNum(a.usage.requests_24h)} / 7 天 ${fmtNum(a.usage.requests_7d)} / 累计 ${fmtNum(a.usage.requests_all)}`}
        >
          <Stat label="24 小时" value={fmtCompact(a.usage.tokens_24h)} />
          <Stat label="7 天" value={fmtCompact(a.usage.tokens_7d)} />
          <Stat label="累计" value={fmtCompact(a.usage.tokens_all)} />
        </div>
      </div>

      <footer className="flex items-center justify-between gap-2 border-t border-slate-100 px-2 py-1.5">
        <span className="truncate pl-2 text-[11px] text-slate-400">
          并发 {a.max_concurrency ?? "默认"}
          {a.usage.last_active ? ` · 最近使用 ${fmtDateTime(a.usage.last_active)}` : " · 还没用过"}
        </span>
        <div className="flex shrink-0 gap-0.5">
          {hasQuota(a.provider) && (
            <ActionButton onClick={onRefresh} disabled={refreshing} title="向上游查询当前额度和订阅">
              刷新
            </ActionButton>
          )}
          <ActionButton onClick={onModels} accent title="查看这个账号有哪些模型，设置哪些可以用">
            模型
          </ActionButton>
          <ActionButton onClick={onEdit}>编辑</ActionButton>
        </div>
      </footer>
    </article>
  );
}

function Dot() {
  return <span className="text-slate-300">·</span>;
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-sm font-semibold tabular-nums text-slate-800">{value}</div>
      <div className="text-[11px] text-slate-400">{label}</div>
    </div>
  );
}

function ActionButton({
  children,
  onClick,
  disabled,
  accent,
  title,
}: {
  children: ReactNode;
  onClick: () => void;
  disabled?: boolean;
  accent?: boolean;
  title?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={
        "rounded-lg px-2.5 py-1 text-xs font-medium disabled:opacity-50 " +
        (accent ? "text-indigo-600 hover:bg-indigo-50" : "text-slate-500 hover:bg-slate-100")
      }
    >
      {children}
    </button>
  );
}

function Subscription({ a, onOpen }: { a: AccountRow; onOpen: () => void }) {
  const s = a.subscription;
  if (!s) {
    return (
      <button type="button" onClick={onOpen} className="flex w-full items-center justify-between rounded-xl border border-dashed border-slate-200 px-3 py-2 text-left text-xs text-slate-500 hover:border-indigo-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500">
        <span>订阅</span>
        <span>尚未获取 · 查看</span>
      </button>
    );
  }
  const v = subscriptionView(s);
  const box =
    v.tone === "bad"
      ? "border-rose-200 bg-rose-50"
      : v.tone === "warn"
        ? "border-amber-200 bg-amber-50"
        : "border-slate-200 bg-white";
  return (
    <button type="button" onClick={onOpen} className={`flex w-full items-center justify-between rounded-xl border px-3 py-2 text-left transition-colors hover:border-indigo-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-500 ${box}`} title="查看订阅详情">
      <span className="text-xs text-slate-400">订阅</span>
      <span className="text-right">
        <span className={`text-sm font-medium ${toneText[v.tone]}`}>{v.main}</span>
        {v.sub && <span className="ml-1.5 text-xs text-slate-400">{v.sub}</span>}
        {v.unverified && <span className="ml-1 text-xs text-amber-500">（未能核实）</span>}
      </span>
    </button>
  );
}

// resetLabel says when a quota window resets: in hours within a day, else the date.
function resetLabel(resetAt: string): string {
  const ms = new Date(resetAt).getTime() - Date.now();
  if (ms <= 0) return "待刷新";
  const h = Math.floor(ms / 3600000);
  const m = Math.floor((ms % 3600000) / 60000);
  if (h < 24) return h > 0 ? `${h} 小时 ${m} 分后重置` : `${m} 分钟后重置`;
  const d = new Date(resetAt);
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")} 重置`;
}

function QuotaMeter({
  label,
  pct,
  resetAt,
  at,
  emptyText,
}: {
  label: string;
  pct: number | null | undefined;
  resetAt: string | null | undefined;
  at: string | undefined;
  emptyText?: string; // shown instead of the bar when there is no reading
}) {
  if (pct == null && emptyText) {
    return (
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="text-slate-500">{label}</span>
        <span className="text-slate-400">{emptyText}</span>
      </div>
    );
  }
  const v = Math.min(Math.max(pct ?? 0, 0), 100);
  const bar =
    v >= 90 ? "bg-rose-500" : v >= 70 ? "bg-amber-500" : "bg-gradient-to-r from-indigo-500 to-violet-500";
  const title = [resetAt && `重置于 ${fmtDateTime(resetAt)}`, at && `更新于 ${fmtDateTime(at)}`].filter(Boolean).join("\n");
  return (
    <div title={title || undefined}>
      <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
        <span className="text-slate-500">{label}</span>
        <span className="truncate tabular-nums">
          <span className="font-semibold text-slate-800">{pct == null ? "—" : `${v.toFixed(0)}%`}</span>
          {resetAt && <span className="ml-1.5 text-slate-400">{resetLabel(resetAt)}</span>}
        </span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-slate-100">
        {pct != null && <div className={`h-full rounded-full ${bar}`} style={{ width: `${v}%` }} />}
      </div>
    </div>
  );
}

function Tags({ a }: { a: AccountRow }) {
  const tags: { text: string; cls: string; title?: string }[] = [];
  if (a.needs_login) tags.push({ text: "需要重新登录", cls: "bg-rose-50 text-rose-600" });
  if (a.allowed_models) tags.push({ text: "限定模型", cls: "bg-sky-50 text-sky-700", title: `只允许：${a.allowed_models.join("、")}` });
  if (a.allow_third_party) tags.push({ text: "第三方客户端", cls: "bg-violet-50 text-violet-700" });
  if (a.cpa_drift) tags.push({ text: "配置漂移", cls: "bg-amber-50 text-amber-700", title: `网关与 CPA 的配置不一致：${a.cpa_drift}` });
  if (tags.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1.5">
      {tags.map((t) => (
        <span key={t.text} title={t.title} className={`rounded-md px-2 py-0.5 text-[11px] font-medium ${t.cls}`}>
          {t.text}
        </span>
      ))}
    </div>
  );
}
