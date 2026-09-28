import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { IPGroupRow } from "../lib/types";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Empty,
  Button,
} from "../components/ui";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";
import { fmtDateTime } from "../lib/format";

export function IPGroupsPage() {
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [editFor, setEditFor] = useState<IPGroupRow | null>(null);

  const groups = useQuery({
    queryKey: ["ip-groups"],
    queryFn: () => api.get<{ ip_groups: IPGroupRow[] }>("/api/admin/ip-groups"),
  });

  const refetch = () => qc.invalidateQueries({ queryKey: ["ip-groups"] });
  const rows = groups.data?.ip_groups ?? [];

  return (
    <div>
      <PageHeader
        title="IP 组"
        desc="账号访问上游时使用的出口。同一组的账号共用一个固定出口 IP，上游每次看到的都是同一个地址。代理地址可以填 socks5 或 http 代理；填 direct 则不走代理，直接用网关服务器自己的 IP（适合试跑）。"
        right={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            新建 IP 组
          </Button>
        }
      />

      {groups.isLoading && <Loading />}
      {groups.error && <ErrorBox error={groups.error} />}

      {groups.data && (
        <div className="grid gap-4 md:grid-cols-2">
          {rows.map((g) => {
            const drift =
              g.last_egress_ip &&
              g.expected_egress_ip &&
              g.last_egress_ip !== g.expected_egress_ip;
            return (
              <Card key={g.id} className="p-5">
                <div className="flex items-start justify-between">
                  <div>
                    <h3 className="text-base font-semibold text-slate-900">
                      {g.name}
                    </h3>
                    <p className="mt-0.5 font-mono text-xs text-slate-400">
                      {g.proxy_masked}
                    </p>
                  </div>
                  <Button onClick={() => setEditFor(g)}>编辑</Button>
                </div>
                <div className="mt-4 grid grid-cols-2 gap-3 text-sm">
                  <Info label="容量" value={`${g.account_count} / ${g.capacity}`} />
                  <Info
                    label="预期出口 IP"
                    value={g.expected_egress_ip || "-"}
                    mono
                  />
                  <Info
                    label="最近出口 IP"
                    value={g.last_egress_ip || "-"}
                    mono
                    warn={!!drift}
                  />
                  <Info
                    label="最近检查"
                    value={g.last_checked_at ? fmtDateTime(g.last_checked_at) : "-"}
                  />
                </div>
                {g.last_check_error && (
                  <div className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-700">
                    {g.last_check_error}
                  </div>
                )}
                {drift && (
                  <div className="mt-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">
                    出口 IP 与预期不一致，可能是风险信号。
                  </div>
                )}
              </Card>
            );
          })}
        </div>
      )}
      {groups.data && rows.length === 0 && <Empty label="暂无 IP 组" />}

      <IPGroupForm
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onDone={refetch}
      />
      <IPGroupForm
        open={!!editFor}
        group={editFor}
        onClose={() => setEditFor(null)}
        onDone={refetch}
      />
    </div>
  );
}

function Info({
  label,
  value,
  mono,
  warn,
}: {
  label: string;
  value: string;
  mono?: boolean;
  warn?: boolean;
}) {
  return (
    <div>
      <div className="text-xs text-slate-400">{label}</div>
      <div
        className={
          "mt-0.5 " +
          (mono ? "font-mono text-xs " : "") +
          (warn ? "text-amber-600" : "text-slate-700")
        }
      >
        {value}
      </div>
    </div>
  );
}

function IPGroupForm({
  open,
  group,
  onClose,
  onDone,
}: {
  open: boolean;
  group?: IPGroupRow | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const { toast } = useToast();
  const editing = !!group;
  const [name, setName] = useState("");
  const [proxy, setProxy] = useState("");
  const [capacity, setCapacity] = useState("");

  // Reset fields when the target group changes.
  const key = group?.id ?? (open ? "new" : "closed");
  const [syncedKey, setSyncedKey] = useState<string | number>("init");
  if (open && syncedKey !== key) {
    setSyncedKey(key);
    setName(group?.name ?? "");
    setProxy("");
    setCapacity(group ? String(group.capacity) : "");
  }

  const save = useMutation({
    mutationFn: () => {
      if (editing) {
        const body: Record<string, unknown> = { capacity: Number(capacity) };
        if (proxy.trim()) body.proxy_url = proxy.trim();
        return api.patch(`/api/admin/ip-groups/${group!.id}`, body);
      }
      return api.post("/api/admin/ip-groups", {
        name: name.trim(),
        proxy_url: proxy.trim(),
        capacity: Number(capacity),
      });
    },
    onSuccess: () => {
      toast(editing ? "已更新" : "已创建", "success");
      onDone();
      onClose();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "保存失败", "error"),
  });

  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";
  const valid = editing
    ? capacity.trim() !== ""
    : name.trim() !== "" && proxy.trim() !== "" && capacity.trim() !== "";

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? `编辑 IP 组 ${group?.name ?? ""}` : "新建 IP 组"}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            onClick={() => save.mutate()}
            disabled={!valid || save.isPending}
          >
            {save.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {!editing && (
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              名称 <span className="text-rose-500">*</span>
            </label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
            />
          </div>
        )}
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            代理地址{" "}
            {editing ? (
              <span className="text-slate-400">（留空则不修改）</span>
            ) : (
              <span className="text-rose-500">*</span>
            )}
          </label>
          <input
            value={proxy}
            onChange={(e) => setProxy(e.target.value)}
            placeholder="socks5://user:pass@host:port，或 direct"
            className={inputCls + " font-mono text-xs"}
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            容量 <span className="text-rose-500">*</span>
          </label>
          <input
            type="number"
            min={1}
            value={capacity}
            onChange={(e) => setCapacity(e.target.value)}
            className={inputCls}
          />
        </div>
        <p className="text-xs text-slate-500">
          推荐每 5～10 个账号配一个固定的住宅或静态 IP 代理。填 direct 表示直接用网关服务器的
          IP 出去：试跑 Kimi 没问题，正式给 ChatGPT 账号用时最好换成固定代理。
        </p>
        <div className="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700">
          修改代理会改变出口 IP，这本身就是一个风险信号，请谨慎操作。
        </div>
      </div>
    </Modal>
  );
}
