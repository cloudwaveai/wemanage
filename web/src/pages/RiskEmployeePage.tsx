import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { api } from "../lib/api";
import type { AdminEmployeeDevicesResp, Device } from "../lib/types";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Button,
} from "../components/ui";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import {
  ActionBtn,
  DevicesTable,
  NetworksTable,
  RiskEventsTable,
  TableCardHeader,
  useResolveRiskEvent,
} from "../components/risk";
import { fmtDateTime } from "../lib/format";

type DeviceAction = "trust" | "block" | "unblock";

const DEVICE_ACTION_LABEL: Record<DeviceAction, string> = {
  trust: "已信任该设备",
  block: "已拉黑该设备",
  unblock: "已解除拉黑，设备回到待确认，需员工重新确认",
};

export function RiskEmployeePage() {
  const { id } = useParams<{ id: string }>();
  const { me, refetch: refetchMe } = useAuth();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [freezeOpen, setFreezeOpen] = useState(false);
  const [unfreezeOpen, setUnfreezeOpen] = useState(false);
  const [reason, setReason] = useState("");

  const q = useQuery({
    queryKey: ["admin-employee-devices", id],
    queryFn: () =>
      api.get<AdminEmployeeDevicesResp>(`/api/admin/employees/${id}/devices`),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["admin-employee-devices", id] });
    qc.invalidateQueries({ queryKey: ["admin-employees"] });
    qc.invalidateQueries({ queryKey: ["risk-events"] });
    // Acting on yourself changes your own banners.
    if (me && String(me.id) === id) refetchMe();
  };

  const deviceAction = useMutation({
    mutationFn: (v: { id: number; action: DeviceAction }) =>
      api.post<{ ok: boolean; status: string }>(
        `/api/admin/devices/${v.id}/${v.action}`
      ),
    onSuccess: (_d, v) => {
      toast(DEVICE_ACTION_LABEL[v.action], "success");
      refresh();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  const freeze = useMutation({
    mutationFn: () =>
      api.post<{ ok: boolean; frozen: boolean }>(
        `/api/admin/employees/${id}/freeze`,
        { reason: reason.trim() }
      ),
    onSuccess: () => {
      toast("已冻结，该员工的 Key 已暂停使用", "success");
      setFreezeOpen(false);
      setReason("");
      refresh();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "冻结失败", "error"),
  });

  const unfreeze = useMutation({
    mutationFn: () =>
      api.post<{ ok: boolean; frozen: boolean }>(
        `/api/admin/employees/${id}/unfreeze`
      ),
    onSuccess: () => {
      toast("已解冻，该员工的 Key 已恢复使用", "success");
      setUnfreezeOpen(false);
      refresh();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "解冻失败", "error"),
  });

  const resolve = useResolveRiskEvent();

  const data = q.data;
  const emp = data?.employee;

  const actionBusy = (d: Device, a: DeviceAction) =>
    deviceAction.isPending &&
    deviceAction.variables?.id === d.id &&
    deviceAction.variables?.action === a;

  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

  return (
    <div>
      <div className="mb-3">
        <Link
          to="/risk"
          className="text-sm font-medium text-indigo-600 hover:text-indigo-500"
        >
          ← 返回风险中心
        </Link>
      </div>

      {q.isLoading && <Loading />}
      {q.error && <ErrorBox error={q.error} />}

      {data && emp && (
        <>
          <PageHeader
            title={emp.name || `员工 #${emp.id}`}
            desc={emp.email || undefined}
            right={
              emp.frozen ? (
                <span className="inline-flex items-center rounded-full bg-rose-100 px-2.5 py-1 text-xs font-medium text-rose-700">
                  已冻结
                </span>
              ) : (
                <>
                  <span className="inline-flex items-center rounded-full bg-emerald-100 px-2.5 py-1 text-xs font-medium text-emerald-700">
                    正常
                  </span>
                  <Button variant="danger" onClick={() => setFreezeOpen(true)}>
                    冻结
                  </Button>
                </>
              )
            }
          />

          <div className="space-y-5">
            {emp.frozen && (
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
                <div>
                  <div>
                    冻结原因：
                    <span className="font-medium">
                      {emp.frozen_reason || "未说明"}
                    </span>
                  </div>
                  <div className="mt-0.5 text-xs text-rose-500">
                    冻结时间：{fmtDateTime(emp.frozen_at)}
                  </div>
                </div>
                <Button variant="primary" onClick={() => setUnfreezeOpen(true)}>
                  解冻
                </Button>
              </div>
            )}

            <Card className="overflow-hidden">
              <TableCardHeader title="设备" sub={`${data.devices.length} 台`} />
              <DevicesTable
                devices={data.devices}
                renderActions={(d) => (
                  <>
                    {d.status !== "trusted" && d.status !== "blocked" && (
                      <ActionBtn
                        tone="primary"
                        disabled={deviceAction.isPending}
                        onClick={() =>
                          deviceAction.mutate({ id: d.id, action: "trust" })
                        }
                      >
                        {actionBusy(d, "trust") ? "处理中…" : "信任"}
                      </ActionBtn>
                    )}
                    {d.status !== "blocked" && (
                      <ActionBtn
                        tone="danger"
                        disabled={deviceAction.isPending}
                        onClick={() =>
                          deviceAction.mutate({ id: d.id, action: "block" })
                        }
                      >
                        {actionBusy(d, "block") ? "处理中…" : "拉黑"}
                      </ActionBtn>
                    )}
                    {d.status === "blocked" && (
                      <ActionBtn
                        tone="neutral"
                        disabled={deviceAction.isPending}
                        onClick={() =>
                          deviceAction.mutate({ id: d.id, action: "unblock" })
                        }
                        title="解除后设备回到待确认，需员工本人重新确认"
                      >
                        {actionBusy(d, "unblock") ? "处理中…" : "解除拉黑"}
                      </ActionBtn>
                    )}
                  </>
                )}
              />
            </Card>

            <Card className="overflow-hidden">
              <TableCardHeader title="网络" sub={`${data.networks.length} 个`} />
              <NetworksTable networks={data.networks} />
            </Card>

            <Card className="overflow-hidden">
              <TableCardHeader title="风险事件" sub={`最近 ${data.events.length} 条`} />
              <RiskEventsTable
                events={data.events}
                showEmployee={false}
                onResolve={(eid) => resolve.mutate(eid)}
                resolvingId={resolve.isPending ? resolve.variables : null}
              />
            </Card>
          </div>
        </>
      )}

      {/* Freeze */}
      <Modal
        open={freezeOpen}
        onClose={() => !freeze.isPending && setFreezeOpen(false)}
        title={`冻结 ${emp?.name ?? ""}`}
        footer={
          <>
            <Button onClick={() => setFreezeOpen(false)} disabled={freeze.isPending}>
              取消
            </Button>
            <Button
              variant="danger"
              onClick={() => freeze.mutate()}
              disabled={freeze.isPending}
            >
              {freeze.isPending ? "处理中…" : "确认冻结"}
            </Button>
          </>
        }
      >
        <p className="mb-3 text-sm text-slate-600">
          冻结后该员工的 Key 会立即停止工作，只有管理员可以解冻。员工会收到通知并看到冻结原因。
        </p>
        <label className="mb-1 block text-xs font-medium text-slate-500">
          冻结原因
        </label>
        <textarea
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={3}
          placeholder="例如：Key 疑似被他人使用，待核实"
          className={inputCls}
        />
        <p className="mt-1 text-xs text-slate-400">
          留空则记为「管理员手动冻结」。
        </p>
      </Modal>

      {/* Unfreeze */}
      <Modal
        open={unfreezeOpen}
        onClose={() => !unfreeze.isPending && setUnfreezeOpen(false)}
        title={`解冻 ${emp?.name ?? ""}？`}
        footer={
          <>
            <Button
              onClick={() => setUnfreezeOpen(false)}
              disabled={unfreeze.isPending}
            >
              取消
            </Button>
            <Button
              variant="primary"
              onClick={() => unfreeze.mutate()}
              disabled={unfreeze.isPending}
            >
              {unfreeze.isPending ? "处理中…" : "确认解冻"}
            </Button>
          </>
        }
      >
        <p className="text-sm text-slate-600">
          解冻后该员工的 Key 会恢复使用。建议先核实情况，并把不认识的设备拉黑。
        </p>
      </Modal>
    </div>
  );
}
