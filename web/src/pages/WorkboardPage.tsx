import { useEffect, useRef, useState } from "react";
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
import { api, ApiError, LIVE_MS } from "../lib/api";
import type { EmployeeWorkUsage, WorkDigests, WorkRange, Workboard } from "../lib/types";
import { Button, Card, ErrorBox, Loading, PageHeader, StatTile } from "../components/ui";
import { useToast } from "../components/Toast";
import { Markdown } from "../lib/md";
import { fmtCompact, fmtDateTime, fmtDayShort, fmtNum } from "../lib/format";
import { providerName } from "../lib/providers";
import { Modal } from "../components/Modal";

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

function RangePicker({ value, onChange, active = true }: { value: WorkRange; onChange: (v: WorkRange) => void; active?: boolean }) {
  return (
    <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
      {RANGES.map((r) => (
        <button
          key={r.v}
          onClick={() => onChange(r.v)}
          className={
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
            (active && value === r.v ? "bg-indigo-600 text-white" : "text-slate-600 hover:bg-slate-100")
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

function EmployeeTable({ board, onSelect }: { board: Workboard; onSelect: (employee: { employee_id: number; name: string }) => void }) {
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
                  <button type="button" onClick={() => onSelect(r)} className="text-left text-indigo-700 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-indigo-600" aria-label={`查看 ${r.name} 的用量详情`}>{r.name}</button>
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
  const [selected, setSelected] = useState<{ employee_id: number; name: string } | null>(null);
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
            <EmployeeTable board={b} onSelect={setSelected} />
            {b.idle.length > 0 && (
              <p className="mt-3 text-xs text-slate-400">
                这段时间没有网关记录（可能使用了其他工具）：{b.idle.map((e, i) => <span key={e.employee_id}>{i > 0 && "、"}<button type="button" className="text-indigo-600 hover:underline" onClick={() => setSelected(e)}>{e.name}</button></span>)}
              </p>
            )}
          </Card>
        </div>
      )}

      <Digests />
      {selected && <EmployeeUsageDialog employee={selected} initialRange={range} onClose={() => setSelected(null)} />}
    </div>
  );
}


function EmployeeUsageDialog({ employee, initialRange, onClose }: { employee: { employee_id: number; name: string }; initialRange: WorkRange; onClose: () => void }) {
  const [range, setRange] = useState<WorkRange>(initialRange);
  const [day, setDay] = useState("");
  const [metric, setMetric] = useState<"tokens" | "requests">("tokens");
  const params = new URLSearchParams(day ? { day } : { range });
  const q = useQuery({
    queryKey: ["employee-work-usage", employee.employee_id, range, day],
    queryFn: () => api.get<EmployeeWorkUsage>(`/api/admin/workboard/employees/${employee.employee_id}?${params}`),
    refetchInterval: LIVE_MS,
    refetchOnWindowFocus: true,
  });
  const d = q.data;
  const chartScroll = useRef<HTMLDivElement>(null);
  useEffect(() => { if (chartScroll.current) chartScroll.current.scrollLeft = chartScroll.current.scrollWidth; }, [d?.from, d?.hourly.length]);
  const axisTime = (value: string, full = false) => {
    const date = new Date(value);
    const options: Intl.DateTimeFormatOptions = full
      ? { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }
      : d?.days === 1 ? { hour: "2-digit", minute: "2-digit", hour12: false } : { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false };
    try { return new Intl.DateTimeFormat("zh-CN", { ...options, timeZone: d?.timezone === "Local" ? undefined : d?.timezone }).format(date); }
    catch { return new Intl.DateTimeFormat("zh-CN", options).format(date); }
  };
  return <Modal open onClose={onClose} width="max-w-5xl" title={`${employee.name} · 使用详情`} footer={<Button onClick={onClose}>关闭</Button>}>
    <div className="flex flex-wrap items-center gap-3">
      <RangePicker value={range} active={!day} onChange={(v) => { setRange(v); setDay(""); }} />
      <label className="flex items-center gap-2 text-sm text-slate-600">指定日期<input type="date" max={ymd(new Date())} value={day} onChange={(e) => setDay(e.target.value)} className="rounded-lg border border-slate-300 px-2 py-1.5 text-sm" /></label>
    </div>
    {q.isLoading && <Loading />}
    {q.error && (q.error instanceof ApiError && q.error.status === 404 ? <p className="mt-4 text-sm text-slate-600">此员工的使用详情不可查看。</p> : <ErrorBox error={q.error} />)}
    {d && <div className="mt-4 space-y-4">
      <p className="text-xs text-slate-500">{axisTime(d.from, true)} 至 {axisTime(d.to, true)} · 按 {d.timezone === "Local" ? "浏览器本地时区" : d.timezone} 统计；仅包含经过网关的记录。</p>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatTile label="Token 合计" value={fmtNum(d.tokens_total)} hint={`${fmtNum(d.requests)} 条记录`} />
        <StatTile label="输入" value={fmtNum(d.tokens_input)} hint={`其中缓存 ${fmtNum(d.tokens_cached)}`} />
        <StatTile label="输出" value={fmtNum(d.tokens_output)} hint={`其中推理 ${fmtNum(d.tokens_reasoning)}`} />
        <StatTile label="会话" value={fmtNum(d.sessions)} hint={`${fmtNum(d.requests)} 条记录`} />
      </div>
      {d.imported_records > 0 && <p className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">其中 {fmtNum(d.imported_records)} 条是本地历史补录，不代表实际 API 调用次数；补录的 Token 为 0。</p>}
      <div className="rounded-lg border border-slate-200 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold text-slate-800">每小时用量</h3><div className="flex gap-1"><Button variant={metric === "tokens" ? "primary" : "ghost"} onClick={() => setMetric("tokens")}>Token</Button><Button variant={metric === "requests" ? "primary" : "ghost"} onClick={() => setMetric("requests")}>记录数</Button></div></div>
        <p className="mt-1 text-xs text-slate-500">每根柱表示一小时。较长时间范围可横向滚动。</p>
        <div ref={chartScroll} className="mt-3 overflow-x-auto"><div style={{ width: Math.max(680, d.hourly.length * 12), height: 240 }}>
          <ResponsiveContainer width="100%" height="100%"><BarChart data={d.hourly} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
            <CartesianGrid stroke="#eef1f6" vertical={false} />
            <XAxis dataKey="t" tickFormatter={(v: string) => axisTime(v)} tick={{ fontSize: 11, fill: "#64748b" }} minTickGap={28} tickLine={false} />
            <YAxis tickFormatter={(v: number) => fmtCompact(v)} tick={{ fontSize: 11, fill: "#64748b" }} width={64} />
            <Tooltip cursor={{ fill: "#f1f5f9" }} labelFormatter={(v) => axisTime(String(v), true)} formatter={(value) => [fmtNum(Number(value)), metric === "tokens" ? "Token" : "记录数"]} />
            <Bar dataKey={metric} fill={BAR} maxBarSize={24} />
          </BarChart></ResponsiveContainer>
        </div></div>
        {d.requests === 0 && <p className="mt-2 text-sm text-slate-500">这段时间没有网关记录。</p>}
      </div>
      <div><h3 className="mb-2 text-sm font-semibold text-slate-800">模型用量</h3>{d.by_model.length ? <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead><tr className="border-b text-xs text-slate-500"><th className="py-2">模型</th><th className="text-right">Token</th><th className="text-right">记录数</th></tr></thead><tbody>{d.by_model.map((m) => <tr key={m.key} className="border-b border-slate-100"><td className="py-2 font-mono text-xs">{!m.key || m.key === "(none)" ? "未记录" : m.key}</td><td className="text-right">{fmtNum(m.tokens_total)}</td><td className="text-right">{fmtNum(m.requests)}</td></tr>)}</tbody></table></div> : <p className="text-sm text-slate-500">没有模型用量记录。</p>}</div>
      <Link className="inline-block text-sm font-medium text-indigo-600 hover:underline" to={`/logs?employee_id=${employee.employee_id}`} onClick={onClose}>查看该员工的请求记录 →</Link>
    </div>}
  </Modal>;
}
