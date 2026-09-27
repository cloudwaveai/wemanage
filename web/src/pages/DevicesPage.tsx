import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { api } from "../lib/api";
import type { Device, MyDevicesResp } from "../lib/types";
import { Card, PageHeader, Loading, ErrorBox, Button } from "../components/ui";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import {
  ActionBtn,
  DevicesTable,
  NetworksTable,
  TableCardHeader,
  deviceName,
} from "../components/risk";

// Pending / expired first so the ones needing action are on top.
const STATUS_ORDER: Record<string, number> = {
  pending: 0,
  expired: 1,
  trusted: 2,
  blocked: 3,
};

export function DevicesPage() {
  const { refetch: refetchMe } = useAuth();
  const qc = useQueryClient();
  const { toast } = useToast();
  const [denyFor, setDenyFor] = useState<Device | null>(null);
  const [keyRevoked, setKeyRevoked] = useState(false);

  const q = useQuery({
    queryKey: ["my-devices"],
    queryFn: () => api.get<MyDevicesResp>("/api/me/devices"),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["my-devices"] });
    refetchMe();
  };

  const confirm = useMutation({
    mutationFn: (id: number) =>
      api.post<{ ok: boolean; status: string }>(`/api/me/devices/${id}/confirm`),
    onSuccess: () => {
      toast("已确认为你本人的设备", "success");
      refresh();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "确认失败", "error"),
  });

  const deny = useMutation({
    mutationFn: (id: number) =>
      api.post<{ ok: boolean; status: string; key_revoked: boolean }>(
        `/api/me/devices/${id}/deny`
      ),
    onSuccess: () => {
      setDenyFor(null);
      setKeyRevoked(true);
      toast("已拉黑该设备，你的 Key 已作废，请重新生成", "success");
      refresh();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "操作失败", "error"),
  });

  const data = q.data;
  const devices = [...(data?.devices ?? [])].sort(
    (a, b) => (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9)
  );
  const networks = data?.networks ?? [];
  const trustedCount = devices.filter((d) => d.status === "trusted").length;
  const busy = confirm.isPending || deny.isPending;

  return (
    <div>
      <PageHeader
        title="我的设备"
        desc="公司会记录你的 Key 在哪些设备和网络上使用，用来发现 Key 外泄。新设备需要你确认是本人；如果不是你，点「不是我」，Key 会立刻作废。"
      />

      {keyRevoked && (
        <div className="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
          <span>
            你的 Key 已作废，所有使用旧 Key 的客户端都已停止工作。请生成新 Key
            并更新到你自己的客户端。
          </span>
          <Link
            to="/key"
            className="inline-flex items-center rounded-lg bg-rose-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-rose-500"
          >
            去生成新 Key
          </Link>
        </div>
      )}

      {q.isLoading && <Loading />}
      {q.error && <ErrorBox error={q.error} />}

      {data && (
        <div className="space-y-5">
          <Card className="overflow-hidden">
            <TableCardHeader
              title="设备"
              sub={`${devices.length} 台`}
              right={
                data.max_trusted > 0 && (
                  <span className="rounded-full bg-indigo-50 px-2.5 py-0.5 text-xs font-medium text-indigo-700">
                    严格模式：最多确认 {data.max_trusted} 台设备（已确认{" "}
                    {trustedCount} 台）
                  </span>
                )
              }
            />
            <DevicesTable
              devices={devices}
              emptyLabel="还没有设备使用过你的 Key"
              statusHint={(d) =>
                d.status === "pending"
                  ? `请在 ${data.confirm_hours} 小时内确认`
                  : null
              }
              renderActions={(d) => (
                <>
                  {(d.status === "pending" || d.status === "expired") && (
                    <ActionBtn
                      tone="primary"
                      onClick={() => confirm.mutate(d.id)}
                      disabled={busy}
                    >
                      {confirm.isPending && confirm.variables === d.id
                        ? "确认中…"
                        : "确认是我"}
                    </ActionBtn>
                  )}
                  {d.status !== "blocked" ? (
                    <ActionBtn
                      tone="danger"
                      onClick={() => setDenyFor(d)}
                      disabled={busy}
                    >
                      不是我
                    </ActionBtn>
                  ) : (
                    <span className="px-2 py-1 text-xs text-slate-400">
                      如有误请联系管理员
                    </span>
                  )}
                </>
              )}
            />
          </Card>

          <Card className="overflow-hidden">
            <TableCardHeader title="网络" sub={`最近 ${networks.length} 个`} />
            <NetworksTable networks={networks} />
          </Card>

          <Card className="p-5">
            <h2 className="mb-3 text-sm font-semibold text-slate-800">
              网络使用建议
            </h2>
            <div className="rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800">
              不要通过会自动切换节点的代理/VPN
              访问网关，请把网关域名设为直连；频繁切换网络可能被误判为多人同时使用。
            </div>
          </Card>
        </div>
      )}

      <Modal
        open={!!denyFor}
        onClose={() => !deny.isPending && setDenyFor(null)}
        title="确认这台设备不是你？"
        footer={
          <>
            <Button onClick={() => setDenyFor(null)} disabled={deny.isPending}>
              取消
            </Button>
            <Button
              variant="danger"
              onClick={() => denyFor && deny.mutate(denyFor.id)}
              disabled={deny.isPending}
            >
              {deny.isPending ? "处理中…" : "不是我，立即作废 Key"}
            </Button>
          </>
        }
      >
        {denyFor && (
          <div className="space-y-3 text-sm text-slate-600">
            <div className="rounded-lg bg-slate-50 px-4 py-3">
              <div className="font-medium text-slate-800">{deviceName(denyFor)}</div>
              <div className="mt-0.5 text-xs text-slate-500">
                最近 IP：
                <span className="font-mono">{denyFor.last_ip || "—"}</span>
              </div>
            </div>
            <div className="rounded-lg bg-rose-50 px-4 py-3 text-rose-700">
              确认后这台设备会被拉黑，<b>你当前的 Key 会立即作废</b>
              ，所有使用这个 Key 的客户端（包括你自己的）都会马上停止工作。之后你需要到「我的
              Key」重新生成一个新 Key，并更新到自己的客户端。此操作不可撤销。
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
