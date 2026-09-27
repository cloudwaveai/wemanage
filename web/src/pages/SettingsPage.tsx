import { useEffect, useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Settings } from "../lib/types";
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

const inputCls =
  "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";

const RISK_MODE_HINTS: Record<Settings["risk_mode"], string> = {
  lax: "发现异常只通知管理员，不冻结；未确认的设备也能使用。",
  standard: "新设备在确认时限内可先使用；高风险行为自动冻结 Key。",
  strict: "新设备必须员工确认后才能使用，每人最多确认 2 台；高风险自动冻结。",
};

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

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<Settings>("/api/admin/settings"),
  });

  useEffect(() => {
    if (data) setForm(structuredClone(data));
  }, [data]);

  const save = useMutation({
    mutationFn: (payload: Settings) => api.put("/api/admin/settings", payload),
    onSuccess: () => {
      toast("设置已保存", "success");
      refetch();
      // A changed notice bumps notice_version, so our own ack is reset too.
      refetchMe();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "保存失败", "error"),
  });

  const patch = (p: Partial<Settings>) =>
    setForm((f) => (f ? { ...f, ...p } : f));

  return (
    <div>
      <PageHeader
        title="设置"
        desc="网关的可调参数。保存后立即生效。"
        right={
          form && (
            <Button
              variant="primary"
              onClick={() => save.mutate(form)}
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
          </Card>

          <Card className="p-5">
            <h2 className="mb-4 text-sm font-semibold text-slate-800">日报</h2>
            <div className="space-y-4">
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="checkbox"
                  checked={form.report_enabled}
                  onChange={(e) => patch({ report_enabled: e.target.checked })}
                  className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                />
                启用日报生成
              </label>
              <div className="max-w-md">
                <label className="mb-1 block text-xs font-medium text-slate-500">
                  日报 LLM 模型
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
              使用告知
            </h2>
            <textarea
              value={form.notice_text}
              onChange={(e) => patch({ notice_text: e.target.value })}
              rows={4}
              className={inputCls}
            />
            <p className="mt-1.5 text-xs text-slate-400">
              当前告知版本：v{form.notice_version}（修改告知内容后会自动
              +1，所有人需要重新确认）
            </p>
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
                  label="允许非 Codex 客户端"
                  checked={form.allow_non_codex}
                  onChange={(v) => patch({ allow_non_codex: v })}
                  hint="关闭时只允许官方 Codex 客户端，第三方工具会被拦截（推荐关闭）"
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
