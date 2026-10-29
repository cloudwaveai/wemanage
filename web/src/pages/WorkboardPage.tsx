import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api, LIVE_MS } from "../lib/api";
import type { WorkDigests, WorkRange, Workboard } from "../lib/types";
import { Button, Card, ErrorBox, Loading, PageHeader, StatTile } from "../components/ui";
import { useToast } from "../components/Toast";
import { Markdown } from "../lib/md";
import { fmtCompact, fmtDateTime, fmtDayShort, fmtNum } from "../lib/format";
import { providerName } from "../lib/providers";

const RANGES: { v: WorkRange; label: string }[] = [
  { v: "today", label: "今天" },
  { v: "yesterday", label: "昨天" },
  { v: "7d", label: "近 7 天" },
  { v: "30d", label: "近 30 天" },
];

const BAR = "#6366f1"; // indigo-500, the app's accent

const pad = (n: number) => String(n).padStart(2, "0");
const ymd = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
function shiftDay(day: string, by: number): string {
  const [y, m, d] = day.split("-").map(Number);
  return ymd(new Date(y, m - 1, d + by));
}
// "10/14 09:05": enough for "last used" in a table cell.
function shortTime(v: string | null): string {
  if (!v) return "-";
  const d = new Date(v);
  return `${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
function dayLabel(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  const date = new Date(y, m - 1, d);
  const today = ymd(new Date());
  const rel = day === today ? "（今天）" : day === shiftDay(today, -1) ? "（昨天）" : "";
  return `${m} 月 ${d} 日 ${WEEKDAYS[date.getDay()]}${rel}`;
}

function RangePicker({ value, onChange }: { value: WorkRange; onChange: (v: WorkRange) => void }) {
  return (
    <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
      {RANGES.map((r) => (
        <button
          key={r.v}
          onClick={() => onChange(r.v)}
          className={
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
            (value === r.v ? "bg-indigo-600 text-white" : "text-slate-600 hover:bg-slate-100")
          }
        >
          {r.label}
        </button>
      ))}
    </div>
  );
}

function DailyTooltip({ active, payload }: { active?: boolean; payload?: { payload: { t: string; tokens: number; requests: number } }[] }) {
  if (!active || !payload?.length) return null;
  const p = payload[0].payload;
  return (
    <div className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs shadow-sm">
      <div className="text-sm font-semibold text-slate-900">{fmtNum(p.tokens)} Token</div>
      <div className="mt-0.5 text-slate-500">
        {fmtDayShort(p.t)} · {fmtNum(p.requests)} 次请求
      </div>
    </div>
  );
}

// Tokens per day: one series, so no legend; the card title names it.
function DailyChart({ data }: { data: Workboard["daily"] }) {
  if (!data.some((p) => p.tokens > 0)) {
    return <p className="py-8 text-center text-sm text-slate-400">这段时间没有用量。</p>;
  }
  return (
    <div className="h-56 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
          <CartesianGrid stroke="#eef1f6" vertical={false} />
          <XAxis
            dataKey="t"
            tickFormatter={(v: string) => fmtDayShort(v)}
            tick={{ fontSize: 12, fill: "#94a3b8" }}
            tickLine={false}
            axisLine={{ stroke: "#e2e8f0" }}
            minTickGap={16}
          />
          <YAxis
            tickFormatter={(v: number) => fmtCompact(v)}
            tick={{ fontSize: 12, fill: "#94a3b8" }}
            tickLine={false}
            axisLine={false}
            width={56}
          />
          <Tooltip content={<DailyTooltip />} cursor={{ fill: "#f1f5f9" }} />
          <Bar dataKey="tokens" fill={BAR} maxBarSize={24} radius={[4, 4, 0, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function EmployeeTable({ board }: { board: Workboard }) {
  const rows = board.employees;
  const multiDay = board.days > 1;
  if (!rows.length) {
    return <p className="py-8 text-center text-sm text-slate-400">这段时间没有网关用量记录。</p>;
  }
  return (
    <div className="scroll-thin -mx-5 overflow-x-auto px-5">
      <table className="w-full min-w-[700px] text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left text-xs font-medium text-slate-400">
            <th className="py-2 pr-3 font-medium">员工</th>
            <th className="py-2 pr-3 font-medium">Token 合计</th>
            <th className="py-2 pr-3 text-right font-medium">输入</th>
            <th className="py-2 pr-3 text-right font-medium">输出</th>
            <th className="py-2 pr-3 text-right font-medium">请求</th>
            <th className="py-2 pr-3 text-right font-medium">会话</th>
            {multiDay && <th className="py-2 pr-3 text-right font-medium">用了几天</th>}
            <th className="py-2 pr-3 font-medium">常用模型</th>
            <th className="py-2 font-medium">最近使用</th>
          </tr>
        </thead>
        <tbody className="tabular-nums">
          {rows.map((r) => (
            <tr key={r.employee_id} className="border-b border-slate-100 last:border-0">
              <td className="py-2.5 pr-3">
                <div className="font-medium text-slate-800">
                  {r.name}
                  {r.is_service && (
                    <span className="ml-1.5 rounded bg-slate-100 px-1.5 py-0.5 text-[11px] font-normal text-slate-500">服务账号</span>
                  )}
                </div>
                {r.department_names.length > 0 && (
                  <div className="text-xs text-slate-400">{r.department_names.join("、")}</div>
                )}
              </td>
              <td className="py-2.5 pr-3">
                <span className="font-medium text-slate-800" title={fmtNum(r.tokens_total)}>{fmtCompact(r.tokens_total)}</span>
              </td>
              <td className="py-2.5 pr-3 text-right text-slate-600" title={`其中缓存 ${fmtNum(r.tokens_cached)}`}>
                {fmtCompact(r.tokens_input)}
              </td>
              <td className="py-2.5 pr-3 text-right text-slate-600" title={`其中推理 ${fmtNum(r.tokens_reasoning)}`}>
                {fmtCompact(r.tokens_output)}
              </td>
              <td className="py-2.5 pr-3 text-right text-slate-600">{fmtNum(r.requests)}</td>
              <td className="py-2.5 pr-3 text-right text-slate-600">{fmtNum(r.sessions)}</td>
              {multiDay && <td className="py-2.5 pr-3 text-right text-slate-600">{r.active_days}</td>}
              <td className="py-2.5 pr-3 font-mono text-xs text-slate-600">{r.top_model || "-"}</td>
              <td className="whitespace-nowrap py-2.5 text-xs text-slate-500">{shortTime(r.last_active)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Digests() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [day, setDay] = useState<string | undefined>(undefined); // undefined: the newest summarized day
  const q = useQuery({
    queryKey: ["work-digests", day ?? ""],
    queryFn: () => api.get<WorkDigests>(`/api/admin/workboard/digests${day ? `?day=${day}` : ""}`),
    refetchInterval: (query) => (query.state.data?.status === "running" ? 4000 : false),
  });
  const run = useMutation({
    mutationFn: (d: string) => api.post("/api/admin/workboard/digests/run", { day: d }),
    onSuccess: () => {
      toast("已开始生成，每人大约半分钟", "success");
      qc.invalidateQueries({ queryKey: ["work-digests"] });
    },
    onError: (e: unknown) => toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  const d = q.data;
  const shown = d?.day ?? day;
  const today = ymd(new Date());
  const running = d?.status === "running";

  return (
    <Card className="mt-5 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-slate-800">每日工作总结</h2>
          <p className="mt-0.5 text-xs text-slate-400">
            AI 根据员工当天在 Codex 里的对话整理。每天 01:30 自动生成前一天的。
          </p>
        </div>
        {shown && (
          <div className="flex items-center gap-1.5">
            <Button variant="ghost" onClick={() => setDay(shiftDay(shown, -1))} aria-label="前一天">
              ←
            </Button>
            <input
              type="date"
              value={shown}
              max={today}
              onChange={(e) => e.target.value && setDay(e.target.value)}
              className="rounded-lg border border-slate-300 px-2.5 py-1.5 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
            />
            <Button variant="ghost" onClick={() => setDay(shiftDay(shown, 1))} disabled={shown >= today} aria-label="后一天">
              →
            </Button>
            {d?.configured && (
              <Button
                variant={d.status === "none" ? "primary" : "secondary"}
                onClick={() => run.mutate(shown)}
                disabled={running || run.isPending}
              >
                {running ? "生成中…" : d.status === "none" ? "生成" : "重新生成"}
              </Button>
            )}
          </div>
        )}
      </div>

      {q.isLoading && <Loading />}
      {q.error && <div className="mt-4"><ErrorBox error={q.error} /></div>}

      {d && (
        <div className={"mt-4 " + (q.isFetching && !running ? "opacity-60 transition-opacity" : "")}>
          <div className="mb-3 text-sm font-medium text-slate-700">{dayLabel(d.day)}</div>

          {!d.configured && (
            <div className="rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800">
              还没选用于总结的账号。到{" "}
              <Link to="/settings" className="font-medium underline">
                设置 → 每日工作总结
              </Link>{" "}
              里选一个 ChatGPT 账号后就会开始生成。
            </div>
          )}

          {running && (
            <div className="flex items-center gap-2 rounded-lg bg-indigo-50 px-4 py-3 text-sm text-indigo-700">
              <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-indigo-200 border-t-indigo-600" />
              正在生成，每人大约半分钟，完成后自动显示。
            </div>
          )}

          {d.status === "failed" && d.error && (
            <div className="mb-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
              生成没有完成：{d.error}
            </div>
          )}
          {d.status === "done" && d.error && (
            <div className="mb-3 rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800">{d.error}</div>
          )}

          {d.configured && d.status === "none" && (
            <p className="py-6 text-center text-sm text-slate-400">这一天还没有总结。</p>
          )}
          {d.status === "done" && !d.overview_md && d.employees.length === 0 && (
            <p className="py-6 text-center text-sm text-slate-400">这一天没有可以总结的对话。</p>
          )}

          {d.overview_md && (
            <div className="rounded-lg bg-slate-50 px-4 py-3">
              <div className="mb-1 text-xs font-medium text-slate-400">团队概览</div>
              <div className="text-sm leading-relaxed text-slate-700">
                <Markdown text={d.overview_md} />
              </div>
            </div>
          )}

          {d.employees.length > 0 && (
            <div className="mt-4 grid gap-4 lg:grid-cols-2">
              {d.employees.map((e) => (
                <div key={e.employee_id} className="rounded-lg border border-slate-200 px-4 py-3">
                  <div className="flex items-baseline justify-between gap-2">
                    <div className="font-medium text-slate-800">
                      {e.name}
                      {e.department_names.length > 0 && (
                        <span className="ml-2 text-xs font-normal text-slate-400">{e.department_names.join("、")}</span>
                      )}
                    </div>
                    <div className="shrink-0 text-xs text-slate-400">
                      {e.sessions} 个会话 · {e.messages} 条消息
                    </div>
                  </div>
                  <div className="mt-2 text-sm leading-relaxed text-slate-700">
                    <Markdown text={e.summary_md} />
                  </div>
                </div>
              ))}
            </div>
          )}

          {d.configured && d.account && (d.status === "done" || d.status === "failed") && (
            <p className="mt-4 text-xs text-slate-400">
              由 {d.account.email}（{providerName("codex")} {d.account.plan_type || ""}）的 {d.model} 整理
              {d.finished_at ? ` · ${fmtDateTime(d.finished_at)}` : ""}
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

export function WorkboardPage() {
  const [range, setRange] = useState<WorkRange>("7d");
  const q = useQuery({
    queryKey: ["workboard", range],
    queryFn: () => api.get<Workboard>(`/api/admin/workboard?range=${range}`),
    placeholderData: (prev) => prev,
    refetchInterval: LIVE_MS, // new requests and quota land within about a minute
    refetchOnWindowFocus: true,
  });
  const b = q.data;
  const t = b?.totals;

  return (
    <div>
      <PageHeader title="工作看板" desc="查看已接入网关的用量和工作记录。样本尚不完整，不能据此评价个人表现或决定压缩成本。" />

      <div className="mb-4">
        <RangePicker value={range} onChange={setRange} />
      </div>

      {q.isLoading && <Loading />}
      {q.error && <ErrorBox error={q.error} />}

      {b && t && (
        <div className={q.isFetching ? "opacity-60 transition-opacity" : ""}>
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            <StatTile label="Token 合计" value={fmtCompact(t.tokens_total)} hint={`${fmtNum(t.requests)} 次请求`} />
            <StatTile label="输入" value={fmtCompact(t.tokens_input)} hint={`其中缓存 ${fmtCompact(t.tokens_cached)}`} />
            <StatTile label="输出" value={fmtCompact(t.tokens_output)} hint={`其中推理 ${fmtCompact(t.tokens_reasoning)}`} />
            <StatTile
              label="使用人数"
              value={t.active_employees}
              hint={b.idle.length ? `另有 ${b.idle.length} 人没有网关记录` : "已有网关记录"}
            />
          </div>

          {b.days > 1 && (
            <Card className="mt-5 p-5">
              <h2 className="mb-3 text-sm font-semibold text-slate-800">每天的 Token 用量</h2>
              <DailyChart data={b.daily} />
            </Card>
          )}

          <Card className="mt-5 p-5">
            <h2 className="mb-3 text-sm font-semibold text-slate-800">每个人的用量</h2>
            <EmployeeTable board={b} />
            {b.idle.length > 0 && (
              <p className="mt-3 text-xs text-slate-400">
                这段时间没有网关记录（可能使用了其他工具）：{b.idle.map((e) => e.name).join("、")}
              </p>
            )}
          </Card>
        </div>
      )}

      <Digests />
    </div>
  );
}
