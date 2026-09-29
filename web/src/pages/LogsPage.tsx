import { useMemo, useState } from "react";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { useAuth } from "../context/AuthContext";
import type { LogList, EmployeeRow } from "../lib/types";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Empty,
  Button,
} from "../components/ui";
import { fmtDateTime, fmtNum, fmtDuration } from "../lib/format";
import { failureReason } from "../lib/failures";

const PAGE_SIZE = 20;

interface Filters {
  employee_id: string;
  from: string; // yyyy-mm-dd
  to: string;
  q: string;
  client: string;
  model: string;
}

const EMPTY: Filters = {
  employee_id: "",
  from: "",
  to: "",
  q: "",
  client: "",
  model: "",
};

const ENDPOINT_LABELS: Record<string, string> = {
  responses: "对话",
  chat: "对话（Chat）",
  images: "生图",
};

function statusTone(code: number): string {
  if (code >= 200 && code < 300) return "bg-emerald-100 text-emerald-700";
  if (code >= 400 && code < 500) return "bg-amber-100 text-amber-700";
  if (code >= 500) return "bg-rose-100 text-rose-700";
  return "bg-slate-100 text-slate-600";
}

export function LogsPage() {
  const { me } = useAuth();
  const navigate = useNavigate();
  const isAdmin = me?.role === "super_admin";

  const [draft, setDraft] = useState<Filters>(EMPTY);
  const [applied, setApplied] = useState<Filters>(EMPTY);
  const [page, setPage] = useState(0);

  const employees = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
    enabled: isAdmin,
  });

  const qs = useMemo(() => {
    const p = new URLSearchParams();
    if (applied.employee_id.trim())
      p.set("employee_id", applied.employee_id.trim());
    if (applied.from) p.set("from", new Date(applied.from + "T00:00:00").toISOString());
    if (applied.to) p.set("to", new Date(applied.to + "T23:59:59").toISOString());
    if (applied.q.trim()) p.set("q", applied.q.trim());
    if (applied.client.trim()) p.set("client", applied.client.trim());
    if (applied.model.trim()) p.set("model", applied.model.trim());
    p.set("limit", String(PAGE_SIZE));
    p.set("offset", String(page * PAGE_SIZE));
    return p.toString();
  }, [applied, page]);

  // Requests are written about a second after they finish; poll while the
  // page is open and visible (react-query pauses in background tabs).
  const logs = useQuery({
    queryKey: ["logs", qs],
    queryFn: () => api.get<LogList>(`/api/logs?${qs}`),
    placeholderData: keepPreviousData,
    refetchInterval: 10_000,
    refetchOnWindowFocus: true,
  });

  const rows = logs.data?.requests ?? [];
  const total = logs.data?.total ?? 0;
  const maxPage = Math.max(0, Math.ceil(total / PAGE_SIZE) - 1);

  const search = () => {
    setPage(0);
    setApplied(draft);
  };
  const reset = () => {
    setDraft(EMPTY);
    setApplied(EMPTY);
    setPage(0);
  };

  const set = (k: keyof Filters, v: string) =>
    setDraft((d) => ({ ...d, [k]: v }));

  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

  return (
    <div>
      <PageHeader
        title={isAdmin ? "对话记录" : "使用日志"}
        desc={
          isAdmin
            ? "全部员工经过网关的请求，每 10 秒自动刷新。点开一条可看原文，查看会写入审计。"
            : "你经过网关的每次调用：时间、模型、状态、Token 和耗时。失败的会写明原因。每 10 秒自动刷新。"
        }
      />

      <Card className="mb-5 p-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {isAdmin && (
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">
                员工
              </label>
              <select
                value={draft.employee_id}
                onChange={(e) => set("employee_id", e.target.value)}
                className={inputCls}
              >
                <option value="">全部员工</option>
                {(employees.data?.employees ?? []).map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name}（{e.id}）
                  </option>
                ))}
              </select>
            </div>
          )}
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              起始日期
            </label>
            <input
              type="date"
              value={draft.from}
              onChange={(e) => set("from", e.target.value)}
              className={inputCls}
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              结束日期
            </label>
            <input
              type="date"
              value={draft.to}
              onChange={(e) => set("to", e.target.value)}
              className={inputCls}
            />
          </div>
          {isAdmin && (
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">
                关键词
              </label>
              <input
                value={draft.q}
                onChange={(e) => set("q", e.target.value)}
                placeholder="原文 / 端点 / 模型…"
                className={inputCls}
              />
            </div>
          )}
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              客户端
            </label>
            <input
              value={draft.client}
              onChange={(e) => set("client", e.target.value)}
              placeholder="如 codex"
              className={inputCls}
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              模型
            </label>
            <input
              value={draft.model}
              onChange={(e) => set("model", e.target.value)}
              placeholder="如 gpt-6-luna"
              className={inputCls}
            />
          </div>
        </div>
        <div className="mt-3 flex gap-2">
          <Button variant="primary" onClick={search}>
            检索
          </Button>
          <Button onClick={reset}>重置</Button>
        </div>
      </Card>

      {logs.isLoading && <Loading />}
      {logs.error && <ErrorBox error={logs.error} />}

      {logs.data && (
        <Card className="overflow-hidden">
          <div className="scroll-thin overflow-x-auto">
            <table className="w-full min-w-[860px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-4 py-2.5 font-medium">时间</th>
                  {isAdmin && <th className="px-4 py-2.5 font-medium">员工</th>}
                  <th className="px-4 py-2.5 font-medium">客户端</th>
                  <th className="px-4 py-2.5 font-medium">类型</th>
                  <th className="px-4 py-2.5 font-medium">模型</th>
                  <th className="px-4 py-2.5 font-medium">状态</th>
                  <th className="px-4 py-2.5 text-right font-medium">输入</th>
                  <th className="px-4 py-2.5 text-right font-medium">输出</th>
                  <th className="px-4 py-2.5 text-right font-medium">耗时</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr
                    key={r.id}
                    onClick={isAdmin ? () => navigate(`/logs/${r.id}`) : undefined}
                    className={
                      "border-b border-slate-100 last:border-0 " +
                      (isAdmin ? "cursor-pointer hover:bg-indigo-50/40" : "hover:bg-slate-50")
                    }
                  >
                    <td className="whitespace-nowrap px-4 py-2.5 text-slate-600">
                      {fmtDateTime(r.started_at)}
                    </td>
                    {isAdmin && (
                      <td className="px-4 py-2.5 text-slate-700">
                        {r.employee_name || r.employee_id}
                      </td>
                    )}
                    <td className="px-4 py-2.5 text-slate-600">
                      {r.client_kind || "-"}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-xs text-slate-500">
                      {ENDPOINT_LABELS[r.endpoint] ?? r.endpoint}
                    </td>
                    <td className="px-4 py-2.5 text-xs text-slate-600">
                      <span className="font-mono">{r.model_requested || "-"}</span>
                      {r.model_responded &&
                        r.model_responded !== r.model_requested && (
                          <span className="text-slate-400">
                            {" → "}
                            {r.model_responded}
                          </span>
                        )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5">
                      <span
                        className={
                          "inline-flex rounded-full px-2 py-0.5 text-xs font-medium " +
                          statusTone(r.status_code)
                        }
                      >
                        {r.status_code || "-"}
                      </span>
                      {failureReason(r.status_code, r.error_type) && (
                        <span className="ml-2 whitespace-nowrap text-xs text-slate-500">
                          {failureReason(r.status_code, r.error_type)}
                        </span>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-slate-600">
                      {fmtNum(r.tokens_input)}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-slate-600">
                      {fmtNum(r.tokens_output)}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-slate-500">
                      {fmtDuration(r.duration_ms)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length === 0 && <Empty label="没有匹配的记录" />}
          <div className="flex items-center justify-between border-t border-slate-100 px-4 py-3 text-sm text-slate-500">
            <span>
              共 {fmtNum(total)} 条 · 第 {page + 1} / {maxPage + 1} 页
            </span>
            <div className="flex gap-2">
              <Button
                onClick={() => setPage((p) => Math.max(0, p - 1))}
                disabled={page === 0}
              >
                上一页
              </Button>
              <Button
                onClick={() => setPage((p) => Math.min(maxPage, p + 1))}
                disabled={page >= maxPage}
              >
                下一页
              </Button>
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}
