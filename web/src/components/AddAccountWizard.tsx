import { useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { IPGroupRow } from "../lib/types";
import { Modal } from "./Modal";
import { Button } from "./ui";
import { useToast } from "./Toast";

type Path = "oauth" | "import";

export function AddAccountWizard({
  open,
  onClose,
  onDone,
}: {
  open: boolean;
  onClose: () => void;
  onDone: () => void;
}) {
  const { toast } = useToast();
  const [ipGroupId, setIpGroupId] = useState<string>("");
  const [os, setOs] = useState("");
  const [terminal, setTerminal] = useState("");
  const [path, setPath] = useState<Path>("oauth");

  // oauth flow state
  const [authUrl, setAuthUrl] = useState<string | null>(null);
  const [state, setState] = useState<string | null>(null);
  const [redirectUrl, setRedirectUrl] = useState("");

  // import flow state
  const [importJson, setImportJson] = useState("");

  const groups = useQuery({
    queryKey: ["ip-groups"],
    queryFn: () => api.get<{ ip_groups: IPGroupRow[] }>("/api/admin/ip-groups"),
    enabled: open,
  });

  const osProfile = () => ({ os: os.trim(), terminal: terminal.trim() });

  const reset = () => {
    setIpGroupId("");
    setOs("");
    setTerminal("");
    setPath("oauth");
    setAuthUrl(null);
    setState(null);
    setRedirectUrl("");
    setImportJson("");
  };

  const close = () => {
    reset();
    onClose();
  };

  const startOAuth = useMutation({
    mutationFn: () =>
      api.post<{ auth_url: string; state: string }>(
        "/api/admin/accounts/oauth-start",
        { ip_group_id: Number(ipGroupId), os_profile: osProfile() }
      ),
    onSuccess: (d) => {
      setAuthUrl(d.auth_url);
      setState(d.state);
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "启动失败", "error"),
  });

  const completeOAuth = useMutation({
    mutationFn: () =>
      api.post<{ account_id: number; email?: string }>(
        "/api/admin/accounts/oauth-complete",
        { state, redirect_url: redirectUrl.trim() }
      ),
    onSuccess: (d) => {
      toast(`账号已添加${d.email ? "：" + d.email : ""}`, "success");
      onDone();
      close();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "完成失败", "error"),
  });

  const importAccount = useMutation({
    mutationFn: () => {
      let content: unknown;
      try {
        content = JSON.parse(importJson);
      } catch {
        throw new Error("授权 JSON 格式无效");
      }
      return api.post<{ account_id: number }>("/api/admin/accounts/import", {
        ip_group_id: Number(ipGroupId),
        os_profile: osProfile(),
        content,
      });
    },
    onSuccess: () => {
      toast("账号已导入", "success");
      onDone();
      close();
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "导入失败", "error"),
  });

  const inputCls =
    "w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100";
  const canProceed = !!ipGroupId;

  return (
    <Modal open={open} onClose={close} title="添加账号" width="max-w-xl">
      {/* Step 1: ip group + os profile */}
      <div className="space-y-4">
        <div>
          <label className="mb-1 block text-xs font-medium text-slate-500">
            IP 组 <span className="text-rose-500">*</span>
          </label>
          <select
            value={ipGroupId}
            onChange={(e) => setIpGroupId(e.target.value)}
            className={inputCls}
          >
            <option value="">请选择 IP 组</option>
            {(groups.data?.ip_groups ?? []).map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}（{g.account_count}/{g.capacity}）
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              操作系统（可选）
            </label>
            <input
              value={os}
              onChange={(e) => setOs(e.target.value)}
              placeholder="如 Mac OS 26.5.2; arm64"
              className={inputCls}
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-slate-500">
              终端（可选）
            </label>
            <input
              value={terminal}
              onChange={(e) => setTerminal(e.target.value)}
              placeholder="如 iTerm.app/3.6.11"
              className={inputCls}
            />
          </div>
        </div>

        {/* path tabs */}
        <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
          {(["oauth", "import"] as Path[]).map((p) => (
            <button
              key={p}
              onClick={() => setPath(p)}
              className={
                "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
                (path === p
                  ? "bg-indigo-600 text-white"
                  : "text-slate-600 hover:bg-slate-100")
              }
            >
              {p === "oauth" ? "OAuth 登录" : "导入授权"}
            </button>
          ))}
        </div>

        {path === "oauth" ? (
          <div className="space-y-3">
            {!authUrl ? (
              <Button
                variant="primary"
                onClick={() => startOAuth.mutate()}
                disabled={!canProceed || startOAuth.isPending}
              >
                {startOAuth.isPending ? "启动中…" : "开始 OAuth 登录"}
              </Button>
            ) : (
              <>
                <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-600">
                  <a
                    href={authUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="break-all font-medium text-indigo-600 hover:text-indigo-500"
                  >
                    在浏览器中登录该 ChatGPT 账号 ↗
                  </a>
                  <p className="mt-2 text-xs leading-relaxed text-slate-500">
                    登录后回调页（localhost:1455）打不开是正常的，复制地址栏完整 URL 粘回下面。
                  </p>
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-slate-500">
                    回调 URL
                  </label>
                  <textarea
                    value={redirectUrl}
                    onChange={(e) => setRedirectUrl(e.target.value)}
                    rows={3}
                    placeholder="http://localhost:1455/auth/callback?code=..."
                    className={inputCls + " font-mono text-xs"}
                  />
                </div>
                <Button
                  variant="primary"
                  onClick={() => completeOAuth.mutate()}
                  disabled={!redirectUrl.trim() || completeOAuth.isPending}
                >
                  {completeOAuth.isPending ? "处理中…" : "完成"}
                </Button>
              </>
            )}
          </div>
        ) : (
          <div className="space-y-3">
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">
                授权 JSON
              </label>
              <textarea
                value={importJson}
                onChange={(e) => setImportJson(e.target.value)}
                rows={6}
                placeholder='{"OPENAI_API_KEY": "...", "tokens": { ... }}'
                className={inputCls + " font-mono text-xs"}
              />
            </div>
            <Button
              variant="primary"
              onClick={() => importAccount.mutate()}
              disabled={
                !canProceed || !importJson.trim() || importAccount.isPending
              }
            >
              {importAccount.isPending ? "导入中…" : "导入账号"}
            </Button>
          </div>
        )}
      </div>
    </Modal>
  );
}
