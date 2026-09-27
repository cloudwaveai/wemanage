import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { EmployeeRow, RiskEvent } from "../lib/types";
import {
  Card,
  PageHeader,
  StatTile,
  Loading,
  ErrorBox,
} from "../components/ui";
import {
  RiskEventsTable,
  TableCardHeader,
  useResolveRiskEvent,
} from "../components/risk";
import { fmtNum } from "../lib/format";

type SeverityFilter = "" | "high" | "warn";

export function RiskPage() {
  const [severity, setSeverity] = useState<SeverityFilter>("");
  const [openOnly, setOpenOnly] = useState(true);

  const qs = useMemo(() => {
    const p = new URLSearchParams();
    if (severity) p.set("severity", severity);
    if (openOnly) p.set("open", "1");
    p.set("limit", "200");
    return p.toString();
  }, [severity, openOnly]);

  const events = useQuery({
    queryKey: ["risk-events", qs],
    queryFn: () =>
      api.get<{ events: RiskEvent[] | null }>(`/api/admin/risk/events?${qs}`),
  });

  // Unfiltered open events, for the stat tiles.
  const openStats = useQuery({
    queryKey: ["risk-events", "open-stats"],
    queryFn: () =>
      api.get<{ events: RiskEvent[] | null }>(
        "/api/admin/risk/events?open=1&limit=500"
      ),
  });

  const employees = useQuery({
    queryKey: ["admin-employees"],
    queryFn: () =>
      api.get<{ employees: EmployeeRow[] }>("/api/admin/employees"),
  });

  const resolve = useResolveRiskEvent();

  const open = openStats.data?.events ?? [];
  const openHigh = open.filter((e) => e.severity === "high").length;
  const openWarn = open.filter((e) => e.severity === "warn").length;
  const frozen = (employees.data?.employees ?? []).filter((e) => e.frozen).length;
  const rows = events.data?.events ?? [];

  const inputCls =
    "rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

  return (
    <div>
      <PageHeader
        title="风险中心"
        desc="Key 外泄 / 共享的可疑信号。点员工姓名可查看其设备、网络并冻结或解冻。"
      />

      <div className="mb-5 grid grid-cols-2 gap-4 lg:grid-cols-3">
        <StatTile
          label="未处理高风险"
          value={openStats.data ? fmtNum(openHigh) : "-"}
        />
        <StatTile
          label="未处理警告"
          value={openStats.data ? fmtNum(openWarn) : "-"}
        />
        <StatTile
          label="当前冻结人数"
          value={employees.data ? fmtNum(frozen) : "-"}
        />
      </div>

      <Card className="overflow-hidden">
        <TableCardHeader
          title="风险事件"
          sub={events.data ? `${rows.length} 条` : undefined}
          right={
            <>
              <select
                value={severity}
                onChange={(e) => setSeverity(e.target.value as SeverityFilter)}
                className={inputCls}
                aria-label="严重度"
              >
                <option value="">严重度：全部</option>
                <option value="high">严重度：高</option>
                <option value="warn">严重度：警告</option>
              </select>
              <label className="flex items-center gap-2 whitespace-nowrap text-sm text-slate-700">
                <input
                  type="checkbox"
                  checked={openOnly}
                  onChange={(e) => setOpenOnly(e.target.checked)}
                  className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                />
                只看未处理
              </label>
            </>
          }
        />
        {events.isLoading && <Loading />}
        {events.error && (
          <div className="p-4">
            <ErrorBox error={events.error} />
          </div>
        )}
        {events.data && (
          <RiskEventsTable
            events={rows}
            onResolve={(id) => resolve.mutate(id)}
            resolvingId={resolve.isPending ? resolve.variables : null}
            emptyLabel={openOnly ? "没有未处理的风险事件" : "暂无风险事件"}
          />
        )}
      </Card>
    </div>
  );
}
