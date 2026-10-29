import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AdminBriefingConfig, BriefingStatus } from "../lib/types";
import { Button, Card, ErrorBox, Loading, PageHeader, Switch } from "../components/ui";
import { useToast } from "../components/Toast";
import { Markdown } from "../lib/md";
import { fmtDateTime } from "../lib/format";

const STATUS_META: Record<BriefingStatus, { label: string; cls: string }> = {
  running: { label: "生成中", cls: "bg-indigo-50 text-indigo-600" },
  test: { label: "已生成", cls: "bg-sky-50 text-sky-600" },
  sent: { label: "已推送", cls: "bg-emerald-50 text-emerald-600" },
  failed: { label: "失败", cls: "bg-rose-50 text-rose-600" },
};
const dayText = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
const parseDay = (s: string) => {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(y, m - 1, d);
};
const spanLabel = (from: string, to: string) => from === to ? from : `${from} 至 ${to}`;

export function BriefingPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [enabled, setEnabled] = useState(true);
  const [prompt, setPrompt] = useState("");
  const [seeded, setSeeded] = useState(false);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const validRange = !!from && !!to && from <= to && parseDay(to).getTime() - parseDay(from).getTime() < 31 * 86400000;
  const q = useQuery({
    queryKey: ["admin-briefing", from, to],
    queryFn: () => api.get<AdminBriefingConfig>(`/api/admin/briefing${from && to ? `?from=${from}&to=${to}` : ""}`),
    enabled: (!from && !to) || validRange,
    refetchInterval: (query) => query.state.data?.running ? 2000 : false,
  });
  const data = q.data;
  useEffect(() => {
    if (data && !seeded) {
      setEnabled(data.enabled);
      setPrompt(data.prompt);
      setFrom(data.from);
      setTo(data.to);
      setSeeded(true);
    }
  }, [data, seeded]);
  const invalidate = () => qc.invalidateQueries({ queryKey: ["admin-briefing"] });
  const save = useMutation({
    mutationFn: () => api.put("/api/admin/briefing", { enabled, prompt }),
    onSuccess: () => { toast("已保存", "success"); invalidate(); },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "保存失败", "error"),
  });
  const generate = useMutation({
    mutationFn: (send: boolean) => api.post(`/api/admin/briefing/${send ? "send" : "preview"}`, { prompt, from, to }),
    onSuccess: (_, send) => {
      toast(send ? "正在生成简报，完成后推送到你的飞书" : "正在生成简报，完成后自动显示", "success");
      invalidate();
    },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "生成失败", "error"),
  });
  const choose = (kind: "yesterday" | "7d" | "week" | "lastWeek") => {
    const today = parseDay(data?.today ?? dayText(new Date()));
    let start = new Date(today), end = new Date(today);
    end.setDate(end.getDate() - 1);
    if (kind === "yesterday") start = new Date(end);
    if (kind === "7d") start.setDate(start.getDate() - 7);
    if (kind === "week" || kind === "lastWeek") {
      start.setDate(start.getDate() - (start.getDay() + 6) % 7);
      if (kind === "lastWeek" || start > end) {
        end = new Date(start);
        end.setDate(end.getDate() - 1);
        start.setDate(start.getDate() - 7);
      }
    }
    setFrom(dayText(start));
    setTo(dayText(end));
  };

  const briefing = data?.briefing;
  const running = data?.running || generate.isPending;
  const dirty = !!data && (data.prompt !== prompt || data.enabled !== enabled);

  return (
    <div>
      <PageHeader title="简报" desc="选择日期范围，按你的侧重点整理团队工作；也可以保留每周五自动推送。每位管理员的设置互不影响。" />
      {q.error && <ErrorBox error={q.error} />}
      {q.isLoading && <Loading />}
      {data && !data.configured && (
        <div className="mb-4 rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800">
          还没选用于总结的账号。到 <Link to="/settings" className="font-medium underline">设置 → 每日工作总结</Link> 里选一个 ChatGPT 账号后即可生成简报。
        </div>
      )}
      <Card className="p-5">
        <h2 className="text-sm font-semibold text-slate-800">生成简报</h2>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <label className="text-sm text-slate-600">开始日期
            <input type="date" aria-label="开始日期" value={from} max={data?.today} disabled={running} onChange={(e) => setFrom(e.target.value)} className="ml-2 rounded-lg border border-slate-300 px-3 py-2" />
          </label>
          <label className="text-sm text-slate-600">结束日期
            <input type="date" aria-label="结束日期" value={to} min={from} max={data?.today} disabled={running} onChange={(e) => setTo(e.target.value)} className="ml-2 rounded-lg border border-slate-300 px-3 py-2" />
          </label>
          {([['yesterday', '昨天'], ['7d', '近 7 天'], ['week', '本周'], ['lastWeek', '上周']] as const).map(([kind, label]) => (
            <Button key={kind} variant="ghost" disabled={running} onClick={() => choose(kind)}>{label}</Button>
          ))}
        </div>
        <p className="mt-2 text-xs text-slate-400">包含开始和结束当天，最多 31 天。缺失或失败的每日总结会自动补齐；包含今天时按生成时已有记录整理。</p>
        {from && to && !validRange && <p className="mt-2 text-sm text-rose-600">请选择先后顺序正确、跨度不超过 31 天的日期。</p>}
        <label className="mt-5 block text-sm font-medium text-slate-700" htmlFor="briefing-prompt">简报侧重点与格式 <span className="ml-2 text-xs font-normal text-slate-400">留空使用默认提示词</span></label>
        <textarea id="briefing-prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={9} maxLength={10000} spellCheck={false}
          placeholder="例如：重点关注交付进展、风险和需要协调的事项，按项目归纳。"
          className="mt-2 w-full rounded-lg border border-slate-300 px-3 py-2 font-mono text-[13px] leading-relaxed text-slate-700 outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100" />
        <details className="mt-1.5 text-xs text-slate-400">
          <summary className="cursor-pointer select-none hover:text-slate-600">查看默认提示词</summary>
          <pre className="mt-2 whitespace-pre-wrap rounded-lg bg-slate-50 p-3 font-mono text-[12px] leading-relaxed">{data?.default_prompt}</pre>
        </details>
        <div className="mt-4 flex flex-wrap items-center gap-2">
          <Button variant="primary" onClick={() => generate.mutate(false)} disabled={!data?.configured || !validRange || running || !!q.error}>
            {running ? "生成中…" : "生成简报"}
          </Button>
          <Button variant="secondary" onClick={() => generate.mutate(true)} disabled={!data?.configured || !validRange || running || !!q.error}>生成并发送到飞书</Button>
          <Button variant="secondary" onClick={() => save.mutate()} disabled={!dirty || save.isPending}>{save.isPending ? "保存中…" : "保存设置"}</Button>
          {dirty && <span className="text-xs text-amber-600">有未保存的设置</span>}
        </div>
        <div className="mt-5 border-t border-slate-100 pt-4">
          <Switch checked={enabled} onChange={setEnabled} label="每周五 10:00 自动生成本周简报并推送给我" disabled={!data?.configured} />
          <p className="mt-2 text-xs text-slate-400">自动推送使用已保存的提示词，覆盖周一至周四。手动生成只覆盖上方选定的日期。
            {data?.account && <> 由 {data.account.email} 的 {data.model} 整理，会占用该账号额度。</>}
          </p>
        </div>
      </Card>
      <Card className="mt-5 p-5">
        <h2 className="text-sm font-semibold text-slate-800">{validRange ? spanLabel(from, to) : "简报内容"}</h2>
        <div className="mt-4">
          {running && <div role="status" className="rounded-lg bg-indigo-50 px-4 py-3 text-sm text-indigo-700">正在整理所选日期的每日总结并生成简报，完成后自动显示；首次补齐可能需要几分钟。</div>}
          {!running && !briefing && <p className="py-6 text-center text-sm text-slate-400">这个日期范围还没有简报。选择日期后点击「生成简报」。</p>}
          {briefing?.status === "failed" && <div className="mb-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">{briefing.error || "生成没有完成，请重试。"}</div>}
          {briefing && briefing.status !== "running" && briefing.content_md && <>
            <div className="mb-2 flex flex-wrap items-center gap-3 text-xs text-slate-400">
              <span className={`rounded-full px-2 py-0.5 font-medium ${STATUS_META[briefing.status].cls}`}>{STATUS_META[briefing.status].label}</span>
              {briefing.generated_at && <span>生成于 {fmtDateTime(briefing.generated_at)}</span>}
              {briefing.sent_at && <span>推送于 {fmtDateTime(briefing.sent_at)}</span>}
              {briefing.model && <span>模型 {briefing.model}</span>}
            </div>
            <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm leading-relaxed text-slate-700"><Markdown text={briefing.content_md} /></div>
          </>}
        </div>
      </Card>
      {!!data?.history?.length && <Card className="mt-5 p-5">
        <h2 className="mb-3 text-sm font-semibold text-slate-800">历史简报</h2>
        <div className="space-y-1">{data.history.map((h) => (
          <button key={`${h.from}/${h.to}`} disabled={running} onClick={() => { setFrom(h.from); setTo(h.to); }}
            className={`flex w-full flex-wrap items-center gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors ${h.from === from && h.to === to ? "bg-indigo-50" : "hover:bg-slate-50"}`}>
            <span className="font-medium text-slate-700">{spanLabel(h.from, h.to)}</span>
            <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_META[h.status].cls}`}>{STATUS_META[h.status].label}</span>
            <span className="ml-auto text-xs text-slate-400">{h.generated_at && fmtDateTime(h.generated_at)}</span>
          </button>
        ))}</div>
      </Card>}
    </div>
  );
}
