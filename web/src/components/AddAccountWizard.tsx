import { useEffect, useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { IPGroupRow, LoginProvider } from "../lib/types";
import { Modal } from "./Modal";
import { Button } from "./ui";
import { CopyButton } from "./CopyButton";
import { useToast } from "./Toast";

type Path = "login" | "import";

// CPA waits this long for a pasted callback after making a login link;
// device logins last as long as their code (expires_in), 15 minutes if unsaid.
const PASTE_WINDOW_MS = 5 * 60 * 1000;
const DEVICE_WINDOW_MS = 15 * 60 * 1000;

interface LoginStart {
  provider: string;
  auth_url: string;
  state: string;
  user_code?: string;
  expires_in?: number;
  paste: boolean;
  callback?: string;
}

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
  const [path, setPath] = useState<Path>("login");
  const [provider, setProvider] = useState("codex");

  // login in progress
  const [login, setLogin] = useState<LoginStart | null>(null);
  const [redirectUrl, setRedirectUrl] = useState("");
  const [linkAt, setLinkAt] = useState(0); // when the login link was made
  const [linkDead, setLinkDead] = useState(false); // CPA gave up on this login
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!login) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [login]);
  const windowMs = login?.paste
    ? PASTE_WINDOW_MS
    : login?.expires_in
      ? login.expires_in * 1000
      : DEVICE_WINDOW_MS;
  const leftMs = linkAt ? linkAt + windowMs - now : windowMs;
  const expired = linkDead || leftMs <= 0;

  // import flow state
  const [importJson, setImportJson] = useState("");

  const groups = useQuery({
    queryKey: ["ip-groups"],
    queryFn: () => api.get<{ ip_groups: IPGroupRow[] }>("/api/admin/ip-groups"),
    enabled: open,
  });
  const providers = useQuery({
    queryKey: ["login-providers"],
    queryFn: () => api.get<{ providers: LoginProvider[] }>("/api/admin/login-providers"),
    enabled: open,
    staleTime: Infinity,
  });
  const current = providers.data?.providers.find((p) => p.id === provider);

  const osProfile = () => ({ os: os.trim(), terminal: terminal.trim() });

  const clearLogin = () => {
    setLogin(null);
    setRedirectUrl("");
    setLinkAt(0);
    setLinkDead(false);
  };

  const reset = () => {
    setIpGroupId("");
    setOs("");
    setTerminal("");
    setPath("login");
    setProvider("codex");
    clearLogin();
    setImportJson("");
  };

  const close = () => {
    reset();
    onClose();
  };

  const start = useMutation({
    mutationFn: () =>
      api.post<LoginStart>("/api/admin/accounts/login-start", {
        provider,
        ip_group_id: Number(ipGroupId),
        os_profile: osProfile(),
      }),
    onSuccess: (d) => {
      setLogin(d);
      setRedirectUrl("");
      setLinkAt(Date.now());
      setNow(Date.now());
      setLinkDead(false);
    },
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "启动失败", "error"),
  });

  const complete = useMutation({
    mutationFn: () =>
      api.post<{ account_id: number; email?: string }>(
        "/api/admin/accounts/login-complete",
        { state: login?.state, redirect_url: redirectUrl.trim() }
      ),
    onSuccess: (d) => {
      toast(`${current?.label ?? ""} 账号已添加${d.email ? "：" + d.email : ""}`, "success");
      onDone();
      close();
    },
    onError: (e: unknown) => {
      // 410: this login is over, only a new link helps.
      if (e instanceof ApiError && e.status === 410) setLinkDead(true);
      toast(e instanceof Error ? e.message : "完成失败", "error");
    },
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
  const countdown = expired
    ? "链接已失效"
    : `${Math.floor(leftMs / 60000)}:${String(Math.floor((leftMs % 60000) / 1000)).padStart(2, "0")} 内有效`;

  return (
    <Modal open={open} onClose={close} title="添加账号" width="max-w-xl">
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
          <p className="mt-1 text-xs text-slate-500">
            账号访问上游时使用的出口 IP。
            {groups.data && groups.data.ip_groups.length === 0 && (
              <>
                还没有 IP 组，请先到{" "}
                <Link
                  to="/ip-groups"
                  onClick={close}
                  className="text-indigo-600 hover:underline"
                >
                  IP 组
                </Link>{" "}
                页新建一个（试跑时代理地址可以填 direct）。
              </>
            )}
          </p>
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

        <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
          {(
            [
              ["login", "登录账号"],
              ["import", "导入授权"],
            ] as [Path, string][]
          ).map(([p, label]) => (
            <button
              key={p}
              onClick={() => setPath(p)}
              className={
                "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
                (path === p ? "bg-indigo-600 text-white" : "text-slate-600 hover:bg-slate-100")
              }
            >
              {label}
            </button>
          ))}
        </div>

        {path === "login" ? (
          <div className="space-y-3">
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">账号类型</label>
              <select
                value={provider}
                onChange={(e) => {
                  setProvider(e.target.value);
                  clearLogin();
                }}
                className={inputCls}
              >
                {(providers.data?.providers ?? [{ id: "codex", label: "ChatGPT", note: "", paste: true }]).map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
              {current?.note && <p className="mt-1 text-xs text-slate-500">{current.note}</p>}
            </div>

            {!login ? (
              <Button
                variant="primary"
                onClick={() => start.mutate()}
                disabled={!canProceed || start.isPending}
              >
                {start.isPending ? "启动中…" : `开始 ${current?.label ?? ""} 登录`}
              </Button>
            ) : login.paste ? (
              <>
                <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-600">
                  <div className="flex flex-wrap items-center gap-2">
                    <CopyButton text={login.auth_url} label="复制登录链接" />
                    <span className="text-slate-500">粘到浏览器的无痕窗口里打开</span>
                    <span className={"ml-auto text-xs " + (expired ? "text-rose-600" : leftMs < 60_000 ? "text-amber-600" : "text-slate-400")}>
                      {countdown}
                    </span>
                  </div>
                  <p className="mt-2 text-xs leading-relaxed text-slate-500">
                    用无痕窗口，避免登录成你平时浏览器里已登录的账号。登录完成后，跳转到的
                    <code className="mx-1 font-mono">{login.callback}</code>
                    页面打不开是正常的，复制地址栏的完整 URL 粘回下面。也可以直接
                    <a
                      href={login.auth_url}
                      target="_blank"
                      rel="noreferrer"
                      className="mx-1 font-medium text-indigo-600 hover:text-indigo-500"
                    >
                      在当前浏览器打开 ↗
                    </a>
                    。
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
                    placeholder={`${login.callback ?? "http://localhost"}?code=...`}
                    className={inputCls + " font-mono text-xs"}
                  />
                </div>
                {expired && (
                  <p className="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-800">
                    {linkDead
                      ? "这次登录已经失效，请重新获取登录链接，用新链接再登录一次。"
                      : "登录链接生成后 5 分钟内要粘回回调地址，现在已经超时。请重新获取登录链接，用新链接再登录一次。"}
                  </p>
                )}
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    variant="primary"
                    onClick={() => complete.mutate()}
                    disabled={!redirectUrl.trim() || expired || complete.isPending}
                  >
                    {complete.isPending ? "处理中…" : "完成"}
                  </Button>
                  <Button
                    variant={expired ? "primary" : "ghost"}
                    onClick={() => start.mutate()}
                    disabled={start.isPending}
                  >
                    {start.isPending ? "获取中…" : "重新获取登录链接"}
                  </Button>
                </div>
              </>
            ) : (
              <>
                <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-600">
                  <div className="flex flex-wrap items-center gap-2">
                    <a
                      href={login.auth_url}
                      target="_blank"
                      rel="noreferrer"
                      className="font-medium text-indigo-600 hover:text-indigo-500"
                    >
                      打开 {current?.label} 授权页面 ↗
                    </a>
                    <CopyButton text={login.auth_url} label="复制链接" />
                    <span className={"ml-auto text-xs " + (expired ? "text-rose-600" : leftMs < 60_000 ? "text-amber-600" : "text-slate-400")}>
                      {countdown}
                    </span>
                  </div>
                  {login.user_code && (
                    <p className="mt-2 text-xs text-slate-500">
                      页面要求输入验证码时填：
                      <span className="ml-1 font-mono text-sm font-semibold text-slate-800">
                        {login.user_code}
                      </span>
                    </p>
                  )}
                  <p className="mt-2 text-xs leading-relaxed text-slate-500">
                    在页面里登录要添加的账号并点"授权"，回到这里点"我已授权，完成"。
                  </p>
                </div>
                {expired && (
                  <p className="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-800">
                    这次授权已经失效，请重新获取链接再授权一次。
                  </p>
                )}
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    variant="primary"
                    onClick={() => complete.mutate()}
                    disabled={expired || complete.isPending}
                  >
                    {complete.isPending ? "确认中…" : "我已授权，完成"}
                  </Button>
                  <Button
                    variant={expired ? "primary" : "ghost"}
                    onClick={() => start.mutate()}
                    disabled={start.isPending}
                  >
                    {start.isPending ? "获取中…" : "重新获取链接"}
                  </Button>
                </div>
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
