import React from "react";
import { Link } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Device, Network, RiskEvent } from "../lib/types";
import {
  DEVICE_STATUS_META,
  SEVERITY_META,
  deviceKindLabel,
  detailText,
  networkLocation,
  ruleLabel,
} from "../lib/risk";
import { fmtDate, fmtDateTime, fmtNum } from "../lib/format";
import { Empty } from "./ui";
import { useToast } from "./Toast";

// Admin: mark a risk event as handled. Shared by RiskPage and RiskEmployeePage.
export function useResolveRiskEvent() {
  const qc = useQueryClient();
  const { toast } = useToast();
  return useMutation({
    mutationFn: (id: number) =>
      api.post<{ ok: boolean }>(`/api/admin/risk/events/${id}/resolve`),
    onSuccess: () => {
      toast("已标记为已处理", "success");
      qc.invalidateQueries({ queryKey: ["risk-events"] });
      qc.invalidateQueries({ queryKey: ["admin-employees"] });
      qc.invalidateQueries({ queryKey: ["admin-employee-devices"] });
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "操作失败", "error"),
  });
}

const pillCls =
  "inline-flex items-center whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium";

export function DeviceStatusBadge({ status }: { status: string }) {
  const meta = DEVICE_STATUS_META[status as Device["status"]];
  return (
    <span className={`${pillCls} ${meta?.cls ?? "bg-slate-100 text-slate-600"}`}>
      {meta?.label ?? status}
    </span>
  );
}

export function SeverityBadge({ severity }: { severity: string }) {
  const meta = SEVERITY_META[severity as RiskEvent["severity"]];
  return (
    <span className={`${pillCls} ${meta?.cls ?? "bg-slate-100 text-slate-600"}`}>
      {meta?.label ?? severity}
    </span>
  );
}

// Small inline row action, same look as the table actions on AccountsPage.
export function ActionBtn({
  tone = "neutral",
  className = "",
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  tone?: "primary" | "neutral" | "danger";
}) {
  const tones = {
    primary: "text-indigo-600 hover:bg-indigo-50",
    neutral: "text-slate-500 hover:bg-slate-100",
    danger: "text-rose-600 hover:bg-rose-50",
  };
  return (
    <button
      className={`whitespace-nowrap rounded px-2 py-1 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-50 ${tones[tone]} ${className}`}
      {...rest}
    />
  );
}

// Title bar for a Card that wraps a table.
export function TableCardHeader({
  title,
  sub,
  right,
}: {
  title: string;
  sub?: React.ReactNode;
  right?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-5 py-3.5">
      <h2 className="text-sm font-semibold text-slate-800">
        {title}
        {sub !== undefined && (
          <span className="ml-2 text-xs font-normal text-slate-400">{sub}</span>
        )}
      </h2>
      {right && <div className="flex items-center gap-2">{right}</div>}
    </div>
  );
}

const thCls = "px-4 py-2.5 font-medium";
const theadRowCls =
  "border-b border-slate-200 bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-400";
const rowCls = "border-b border-slate-100 last:border-0 hover:bg-slate-50";

// ---------------------------------------------------------------- devices
export function deviceName(d: Device): string {
  return d.label || d.client_kind || `设备 #${d.id}`;
}

export function DevicesTable({
  devices,
  renderActions,
  statusHint,
  emptyLabel = "暂无设备记录",
}: {
  devices: Device[];
  renderActions: (d: Device) => React.ReactNode;
  statusHint?: (d: Device) => React.ReactNode;
  emptyLabel?: string;
}) {
  return (
    <>
      <div className="scroll-thin overflow-x-auto">
        <table className="w-full min-w-[700px] text-sm">
          <thead>
            <tr className={theadRowCls}>
              <th className={thCls}>设备</th>
              <th className={thCls}>状态</th>
              <th className={thCls}>使用时间</th>
              <th className={thCls}>最近 IP</th>
              <th className={thCls + " text-right"}>操作</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => {
              const hint = statusHint?.(d);
              return (
                <tr key={d.id} className={rowCls}>
                  <td className="max-w-[200px] px-4 py-2.5">
                    <div className="truncate font-medium text-slate-800" title={deviceName(d)}>
                      {deviceName(d)}
                    </div>
                    <div
                      className="truncate text-[11px] text-slate-400"
                      title={d.user_agent || undefined}
                    >
                      {deviceKindLabel(d.kind)}
                      {d.user_agent && (
                        <span className="font-mono"> · {d.user_agent}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    <DeviceStatusBadge status={d.status} />
                    {hint && (
                      <div className="mt-1 max-w-[120px] text-[11px] leading-4 text-amber-600">
                        {hint}
                      </div>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-slate-600">
                    <div>{fmtDateTime(d.last_seen)}</div>
                    <div
                      className="text-[11px] text-slate-400"
                      title={fmtDateTime(d.first_seen)}
                    >
                      首次 {fmtDate(d.first_seen)}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">
                    <div className="font-mono text-xs text-slate-600">{d.last_ip || "—"}</div>
                    <div className="text-[11px] tabular-nums text-slate-400">
                      {fmtNum(d.requests)} 次请求
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right">
                    <div className="flex justify-end gap-1">{renderActions(d)}</div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {devices.length === 0 && <Empty label={emptyLabel} />}
    </>
  );
}

// ---------------------------------------------------------------- networks
export function NetworksTable({
  networks,
  emptyLabel = "暂无网络记录",
}: {
  networks: Network[];
  emptyLabel?: string;
}) {
  return (
    <>
      <div className="scroll-thin overflow-x-auto">
        <table className="w-full min-w-[720px] text-sm">
          <thead>
            <tr className={theadRowCls}>
              <th className={thCls}>位置</th>
              <th className={thCls}>IP</th>
              <th className={thCls}>运营商</th>
              <th className={thCls}>最近使用</th>
              <th className={thCls + " text-right"}>次数</th>
            </tr>
          </thead>
          <tbody>
            {networks.map((n) => {
              const loc = networkLocation(n);
              return (
                <tr key={n.id} className={rowCls}>
                  <td className="px-4 py-2.5 text-slate-700">
                    <div className="flex items-center gap-1.5">
                      <span>{loc || "—"}</span>
                      {n.datacenter && (
                        <span
                          className={`${pillCls} bg-rose-100 text-rose-700`}
                          title="来自机房/云服务器 IP，常见于代理或 VPN"
                        >
                          机房
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-slate-600">
                    {n.ip_sample || "—"}
                  </td>
                  <td className="px-4 py-2.5 text-slate-600">{n.isp || "—"}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-slate-600">
                    {fmtDateTime(n.last_seen)}
                  </td>
                  <td className="px-4 py-2.5 text-right tabular-nums text-slate-600">
                    {fmtNum(n.requests)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {networks.length === 0 && <Empty label={emptyLabel} />}
    </>
  );
}

// ---------------------------------------------------------------- risk events
export function RiskEventRow({
  ev,
  showEmployee,
  onResolve,
  resolving,
}: {
  ev: RiskEvent;
  showEmployee: boolean;
  onResolve: (id: number) => void;
  resolving: boolean;
}) {
  const detail = detailText(ev.detail);
  return (
    <tr className={rowCls}>
      <td className="whitespace-nowrap px-4 py-2.5 text-slate-600">
        {fmtDateTime(ev.at)}
      </td>
      {showEmployee && (
        <td className="whitespace-nowrap px-4 py-2.5">
          <Link
            to={`/risk/employees/${ev.employee_id}`}
            className="font-medium text-indigo-600 hover:text-indigo-500"
          >
            {ev.employee_name || `#${ev.employee_id}`}
          </Link>
        </td>
      )}
      <td className="whitespace-nowrap px-4 py-2.5 text-slate-700">
        <span className="inline-flex items-center gap-2">
          <SeverityBadge severity={ev.severity} />
          {ruleLabel(ev.rule)}
        </span>
      </td>
      <td className="max-w-[200px] px-4 py-2.5">
        {detail ? (
          <span title={detail} className="block truncate text-xs text-slate-500">
            {detail}
          </span>
        ) : (
          <span className="text-slate-300">-</span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right">
        {ev.resolved_at ? (
          <span
            className="px-2 text-xs text-slate-400"
            title={`处理于 ${fmtDateTime(ev.resolved_at)}`}
          >
            已处理
          </span>
        ) : (
          <ActionBtn
            tone="primary"
            onClick={() => onResolve(ev.id)}
            disabled={resolving}
          >
            {resolving ? "处理中…" : "标记已处理"}
          </ActionBtn>
        )}
      </td>
    </tr>
  );
}

export function RiskEventsTable({
  events,
  showEmployee = true,
  onResolve,
  resolvingId,
  emptyLabel = "暂无风险事件",
}: {
  events: RiskEvent[];
  showEmployee?: boolean;
  onResolve: (id: number) => void;
  resolvingId?: number | null;
  emptyLabel?: string;
}) {
  return (
    <>
      <div className="scroll-thin overflow-x-auto">
        <table className="w-full min-w-[720px] text-sm">
          <thead>
            <tr className={theadRowCls}>
              <th className={thCls}>时间</th>
              {showEmployee && <th className={thCls}>员工</th>}
              <th className={thCls}>规则</th>
              <th className={thCls}>详情</th>
              <th className={thCls + " text-right"}>处理</th>
            </tr>
          </thead>
          <tbody>
            {events.map((ev) => (
              <RiskEventRow
                key={ev.id}
                ev={ev}
                showEmployee={showEmployee}
                onResolve={onResolve}
                resolving={resolvingId === ev.id}
              />
            ))}
          </tbody>
        </table>
      </div>
      {events.length === 0 && <Empty label={emptyLabel} />}
    </>
  );
}
