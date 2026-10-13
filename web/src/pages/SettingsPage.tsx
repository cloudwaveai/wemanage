import { useEffect, useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccountRow, ContentAuditMode, ContentAuditRule, Settings } from "../lib/types";
import { isChatGPT } from "../lib/providers";
import {
  Card,
  PageHeader,
  Loading,
  ErrorBox,
  Button,
} from "../components/ui";
import { useToast } from "../components/Toast";
import { useAuth } from "../context/AuthContext";
import { RISK_MODES, RISK_MODE_LABELS } from "../lib/risk";
import { ActionBtn } from "../components/risk";

const inputCls =
  "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

const RISK_MODE_HINTS: Record<Settings["risk_mode"], string> = {
  lax: "发现异常只通知管理员，不冻结；未确认的设备也能使用。",
  standard: "新设备在确认时限内可先使用；高风险行为自动冻结 Key。",
  strict: "新设备必须员工确认后才能使用，每人最多确认 2 台；高风险自动冻结。",
};

const AUDIT_MODES: { v: ContentAuditMode; label: string; hint: string }[] = [
  { v: "off", label: "关闭", hint: "不检查请求内容。" },
  { v: "warn", label: "仅提醒（推荐）", hint: "放行请求，通知管理员。" },
  { v: "block", label: "拦截", hint: "直接拒绝请求并通知管理员。" },
];

// Textarea → office_networks: one entry per line (commas also split), trimmed,
// empties dropped. The server validates each CIDR / IP.
function parseNetworks(text: string): string[] {
  return text
    .split(/[\n,，]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function NumberField({
  label,
  value,
  onChange,
  hint,
  min,
  max,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  hint?: string;
  min?: number;
  max?: number;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium text-slate-500">
        {label}
      </label>
      <input
        type="number"
        min={min}
        max={max}
        value={Number.isFinite(value) ? value : ""}
        onChange={(e) => onChange(Number(e.target.value))}
        className={inputCls}
      />
      {hint && <p className="mt-1 text-xs text-slate-400">{hint}</p>}
    </div>
  );
}

function ToggleField({
  label,
  checked,
  onChange,
  hint,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  hint?: string;
}) {
  return (
    <div>
      <label className="flex items-center gap-2 text-sm text-slate-700">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
        />
        {label}
      </label>
      {hint && <p className="mt-1 pl-6 text-xs text-slate-400">{hint}</p>}
    </div>
  );
}

export function SettingsPage() {
  const { toast } = useToast();
  const { refetch: refetchMe } = useAuth();
  const [form, setForm] = useState<Settings | null>(null);
  // Office networks are edited as free text and parsed on save, so typing a
  // newline doesn't get eaten mid-edit.
  const [netText, setNetText] = useState("");

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<Settings>("/api/admin/settings"),
  });

  useEffect(() => {
    if (data) {
      setForm(structuredClone(data));
      setNetText((data.office_networks ?? []).join("\n"));
    }
  }, [data]);

  const save = useMutation({
    mutationFn: (payload: Settings) => api.put("/api/admin/settings", payload),
    onSuccess: () => {
      toast("设置已保存", "success");
      refetch();
      refetchMe();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "保存失败", "error"),
  });

  // ChatGPT accounts that can write the work board's daily summaries.
  const accounts = useQuery({
    queryKey: ["admin-accounts"],
    queryFn: () => api.get<{ accounts: AccountRow[] }>("/api/admin/accounts"),
  });
  const chatgptAccounts = (accounts.data?.accounts ?? []).filter((a) => isChatGPT(a.provider));

  const patch = (p: Partial<Settings>) =>
    setForm((f) => (f ? { ...f, ...p } : f));

  const rules: ContentAuditRule[] = form?.content_audit_rules ?? [];
  const setRules = (r: ContentAuditRule[]) => patch({ content_audit_rules: r });
  const setRule = (i: number, p: Partial<ContentAuditRule>) =>
    setRules(rules.map((r, j) => (j === i ? { ...r, ...p } : r)));

  const onSave = () => {
    if (!form) return;
    // Drop rows left completely blank; an empty regex would match every
    // message, so refuse a named rule without a pattern.
    const auditRules = rules
      .map((r) => ({ name: r.name.trim(), pattern: r.pattern }))
      .filter((r) => r.name || r.pattern.trim());
    const noPattern = auditRules.find((r) => !r.pattern.trim());
    if (noPattern) {
      toast(`内容审核规则「${noPattern.name}」的正则不能为空`, "error");
      return;
    }
    save.mutate({
      ...form,
      office_networks: parseNetworks(netText),
      content_audit_rules: auditRules,
    });
  };

  return (
    <div>
      <PageHeader
        title="设置"
        desc="网关的可调参数。保存后立即生效。"
        right={
          form && (
            <Button
              variant="primary"
              onClick={onSave}
              disabled={save.isPending}
            >
              {save.isPending ? "保存中…" : "保存设置"}
            </Button>
          )
        }
      />

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}

      {form && (
        <div className="space-y-5">
          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">基础</h2>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <NumberField
                label="默认最大并发"
                value={form.default_max_concurrency}
                onChange={(v) => patch({ default_max_concurrency: v })}
                hint="1 – 32"
              />
              <NumberField
                label="排队超时（秒）"
                value={form.queue_timeout_seconds}
                onChange={(v) => patch({ queue_timeout_seconds: v })}
                hint="1 – 600"
              />
              <NumberField
                label="原文保留天数"
                value={form.retention_days}
                onChange={(v) => patch({ retention_days: v })}
                hint="7 – 3650"
              />
              <NumberField
                label="模型不一致阈值"
                value={form.model_mismatch_threshold}
                onChange={(v) => patch({ model_mismatch_threshold: v })}
                hint="≥ 1"
              />
            </div>
            <div className="mt-5 max-w-md">
              <label className="mb-1 block text-xs font-medium text-slate-500">
                员工配置 Codex 时推荐的模型（ChatGPT 账号）
              </label>
              <input
                value={form.default_codex_model ?? ""}
                onChange={(e) => patch({ default_codex_model: e.target.value.trim() })}
                placeholder="gpt-6-luna"
                className="w-full rounded-lg border border-slate-300 px-3 py-2 font-mono text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
              />
              <p className="mt-1 text-xs text-slate-400">
                员工生成 Key 时的配置里默认选中这个模型。账号没有这个模型时，自动选同系列（如 luna）里最新的。
              </p>
            </div>
            <div className="mt-5">
              <ToggleField
                label="关闭 WebSocket（全部改走 HTTP）"
                checked={!!form.disable_websocket}
                onChange={(v) => patch({ disable_websocket: v })}
                hint="默认开启 WebSocket：与官方 Codex 连接 OpenAI 的方式一致。出现兼容问题时可临时关闭，员工的 Codex 会自动退回 HTTP，无需改配置。"
              />
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-1 text-sm font-semibold text-slate-800">每日工作总结</h2>
            <p className="mb-4 text-xs text-slate-400">
              「工作看板」里每天的工作总结用哪个账号来写。每天 01:30 根据前一天的对话生成，会用掉这个账号的一些额度；不选就不生成。
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">用哪个账号</label>
                <select
                  value={form.work_digest_account_id ?? 0}
                  onChange={(e) => patch({ work_digest_account_id: Number(e.target.value) })}
                  className={inputCls}
                >
                  <option value={0}>不生成</option>
                  {chatgptAccounts.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.email}（{a.plan_type || "ChatGPT"}）
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">模型</label>
                <input
                  value={form.work_digest_model ?? ""}
                  onChange={(e) => patch({ work_digest_model: e.target.value.trim() })}
                  placeholder={`留空则用 ${form.default_codex_model || "gpt-6-luna"}`}
                  className={inputCls + " font-mono"}
                />
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">周报</h2>
            <div className="space-y-4">
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="checkbox"
                  checked={form.report_enabled}
                  onChange={(e) => patch({ report_enabled: e.target.checked })}
                  className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                />
                启用自动周报（每周日 16:00 根据员工本周的使用自动编写本周周报草稿，只发给员工本人，员工可自行关闭）
              </label>
              <div className="max-w-md">
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  周报 LLM 模型
                </label>
                <input
                  value={form.report_llm_model}
                  onChange={(e) => patch({ report_llm_model: e.target.value })}
                  placeholder="留空则使用默认"
                  className={inputCls}
                />
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">
              风险控制
            </h2>
            <div className="space-y-5">
              <div>
                <div className="mb-1.5 text-xs font-medium text-slate-500">
                  风控模式
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {RISK_MODES.map((m) => {
                    const active = form.risk_mode === m;
                    return (
                      <label
                        key={m}
                        className={
                          "flex cursor-pointer items-start gap-2 rounded-lg border px-3 py-2.5 transition-colors " +
                          (active
                            ? "border-indigo-400 bg-indigo-50"
                            : "border-slate-300 hover:bg-slate-50")
                        }
                      >
                        <input
                          type="radio"
                          name="risk_mode"
                          value={m}
                          checked={active}
                          onChange={() => patch({ risk_mode: m })}
                          className="mt-0.5 h-4 w-4 border-slate-300 text-indigo-600 focus:ring-indigo-500"
                        />
                        <span>
                          <span
                            className={
                              "block text-sm font-medium " +
                              (active ? "text-indigo-700" : "text-slate-700")
                            }
                          >
                            {RISK_MODE_LABELS[m]}
                          </span>
                          <span className="mt-0.5 block text-xs text-slate-400">
                            {RISK_MODE_HINTS[m]}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </div>
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                <ToggleField
                  label="所有账号都允许非 Codex 客户端"
                  checked={form.allow_non_codex}
                  onChange={(v) => patch({ allow_non_codex: v })}
                  hint="总开关（推荐关闭）。只想给个别账号开放 Claude Code、OpenCode、Command Code 等工具，到「账号 → 编辑」里单独打开"
                />
                <ToggleField
                  label="要求 Codex 安装 ID"
                  checked={form.require_codex_installation_id}
                  onChange={(v) => patch({ require_codex_installation_id: v })}
                  hint="先观察一段时间，确认所有 Codex 请求都带安装 ID 后再开启"
                />
              </div>

              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <NumberField
                  label="新设备确认时限（小时）"
                  value={form.device_confirm_hours}
                  onChange={(v) => patch({ device_confirm_hours: v })}
                  min={1}
                  max={720}
                  hint="1 – 720，逾期未确认的设备会暂停"
                />
                <NumberField
                  label="同时使用检测窗口（分钟）"
                  value={form.concurrency_window_minutes}
                  onChange={(v) => patch({ concurrency_window_minutes: v })}
                  min={1}
                  max={240}
                  hint="1 – 240，窗口内多个网络使用视为同时使用"
                />
                <NumberField
                  label="新设备宽限期（分钟）"
                  value={form.new_device_grace_minutes}
                  onChange={(v) => patch({ new_device_grace_minutes: v })}
                  min={1}
                  max={1440}
                  hint="1 – 1440，期间未确认设备不参与异地同时使用判定"
                />
                <NumberField
                  label="最多活跃设备数（7 天）"
                  value={form.max_active_devices}
                  onChange={(v) => patch({ max_active_devices: v })}
                  min={1}
                  max={50}
                  hint="1 – 50，超过会产生警告"
                />
                <NumberField
                  label="一个账号最多几人共用"
                  value={form.max_account_members}
                  onChange={(v) => patch({ max_account_members: v })}
                  min={1}
                  max={10}
                  hint="1 – 10，默认 3；已经超出的账号不受影响，只是不能再加人"
                />
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">
              公司网络限制
            </h2>
            <div className="space-y-4">
              <ToggleField
                label="只允许在公司网络内使用"
                checked={form.office_only}
                onChange={(v) => patch({ office_only: v })}
                hint="开启后，员工的 Key 只能从下面这些公司出口 IP 使用；需要在外办公的人可以在「风险中心 → 员工详情」里单独放开。"
              />
              <div className="max-w-md">
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  公司出口 IP / 网段（每行一个）
                </label>
                <textarea
                  value={netText}
                  onChange={(e) => setNetText(e.target.value)}
                  rows={4}
                  placeholder={"203.0.113.7\n10.0.0.0/8"}
                  className={inputCls + " scroll-thin font-mono text-xs"}
                />
                <p className="mt-1 text-xs text-slate-400">
                  支持单个 IP 或 CIDR 网段。开启限制时至少要填一个。
                </p>
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-1 text-sm font-semibold text-slate-800">
              内容审核
            </h2>
            <p className="mb-4 text-xs leading-5 text-slate-400">
              在请求发给 OpenAI 之前检查员工最新一条消息。OpenAI
              会因为恶意软件、盗号、免杀、DDoS
              等网络滥用直接封号，这里的规则就是为了提前发现。只检查最新一条消息，不检查历史上下文和工具输出。
            </p>
            <div className="space-y-5">
              <div>
                <div className="mb-1.5 text-xs font-medium text-slate-500">
                  审核模式
                </div>
                <div className="grid gap-2 sm:grid-cols-3">
                  {AUDIT_MODES.map((m) => {
                    const active = form.content_audit_mode === m.v;
                    return (
                      <label
                        key={m.v}
                        className={
                          "flex cursor-pointer items-start gap-2 rounded-lg border px-3 py-2.5 transition-colors " +
                          (active
                            ? "border-indigo-400 bg-indigo-50"
                            : "border-slate-300 hover:bg-slate-50")
                        }
                      >
                        <input
                          type="radio"
                          name="content_audit_mode"
                          value={m.v}
                          checked={active}
                          onChange={() => patch({ content_audit_mode: m.v })}
                          className="mt-0.5 h-4 w-4 border-slate-300 text-indigo-600 focus:ring-indigo-500"
                        />
                        <span>
                          <span
                            className={
                              "block text-sm font-medium " +
                              (active ? "text-indigo-700" : "text-slate-700")
                            }
                          >
                            {m.label}
                          </span>
                          <span className="mt-0.5 block text-xs text-slate-400">
                            {m.hint}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </div>
              </div>

              <div>
                <div className="mb-1.5 text-xs font-medium text-slate-500">
                  审核规则
                  <span className="ml-1.5 font-normal text-slate-400">
                    正则表达式，不区分大小写
                  </span>
                </div>
                {rules.length > 0 ? (
                  <div className="overflow-hidden rounded-lg border border-slate-200">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b border-slate-200 bg-slate-50 text-left text-xs text-slate-400">
                          <th className="w-1/3 px-3 py-2 font-medium">名称</th>
                          <th className="px-3 py-2 font-medium">正则</th>
                          <th className="w-14 px-3 py-2" />
                        </tr>
                      </thead>
                      <tbody>
                        {rules.map((r, i) => (
                          <tr
                            key={i}
                            className="border-b border-slate-100 last:border-0"
                          >
                            <td className="px-3 py-1.5">
                              <input
                                value={r.name}
                                onChange={(e) => setRule(i, { name: e.target.value })}
                                placeholder="例如：恶意软件"
                                className={inputCls + " py-1.5"}
                              />
                            </td>
                            <td className="px-3 py-1.5">
                              <input
                                value={r.pattern}
                                onChange={(e) =>
                                  setRule(i, { pattern: e.target.value })
                                }
                                placeholder="例如：ransomware|勒索软件"
                                spellCheck={false}
                                className={inputCls + " py-1.5 font-mono text-xs"}
                              />
                            </td>
                            <td className="px-3 py-1.5 text-right">
                              <ActionBtn
                                tone="danger"
                                onClick={() =>
                                  setRules(rules.filter((_, j) => j !== i))
                                }
                              >
                                删除
                              </ActionBtn>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="rounded-lg border border-dashed border-slate-300 px-3 py-4 text-center text-xs text-slate-400">
                    暂无规则
                  </div>
                )}
                <Button
                  className="mt-2"
                  onClick={() => setRules([...rules, { name: "", pattern: "" }])}
                >
                  添加规则
                </Button>
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">
              Codex 客户端画像
            </h2>
            <div className="grid gap-4 sm:grid-cols-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  版本
                </label>
                <input
                  value={form.codex_profile.version}
                  onChange={(e) =>
                    patch({
                      codex_profile: {
                        ...form.codex_profile,
                        version: e.target.value,
                      },
                    })
                  }
                  className={inputCls}
                />
              </div>
              <div className="sm:col-span-2">
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  UA 模板
                </label>
                <input
                  value={form.codex_profile.ua_template}
                  onChange={(e) =>
                    patch({
                      codex_profile: {
                        ...form.codex_profile,
                        ua_template: e.target.value,
                      },
                    })
                  }
                  className={inputCls + " font-mono text-xs"}
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  Originator
                </label>
                <input
                  value={form.codex_profile.originator}
                  onChange={(e) =>
                    patch({
                      codex_profile: {
                        ...form.codex_profile,
                        originator: e.target.value,
                      },
                    })
                  }
                  className={inputCls}
                />
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">
              默认设备画像
            </h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  操作系统
                </label>
                <input
                  value={form.default_device_profile.os}
                  onChange={(e) =>
                    patch({
                      default_device_profile: {
                        ...form.default_device_profile,
                        os: e.target.value,
                      },
                    })
                  }
                  className={inputCls}
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  终端
                </label>
                <input
                  value={form.default_device_profile.terminal}
                  onChange={(e) =>
                    patch({
                      default_device_profile: {
                        ...form.default_device_profile,
                        terminal: e.target.value,
                      },
                    })
                  }
                  className={inputCls}
                />
              </div>
            </div>
          </Card>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card className="p-5">
              <h2 className="mb-2 text-sm font-semibold text-slate-800">
                客户端识别规则
                <span className="ml-2 text-xs font-normal text-slate-400">
                  只读
                </span>
              </h2>
              <textarea
                readOnly
                value={JSON.stringify(form.client_rules ?? [], null, 2)}
                rows={8}
                className={
                  inputCls +
                  " scroll-thin cursor-default bg-slate-50 font-mono text-xs text-slate-600"
                }
              />
            </Card>
            <Card className="p-5">
              <h2 className="mb-2 text-sm font-semibold text-slate-800">
                脱敏规则
                <span className="ml-2 text-xs font-normal text-slate-400">
                  只读
                </span>
              </h2>
              <textarea
                readOnly
                value={JSON.stringify(form.redaction_rules ?? [], null, 2)}
                rows={8}
                className={
                  inputCls +
                  " scroll-thin cursor-default bg-slate-50 font-mono text-xs text-slate-600"
                }
              />
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}
