import { useEffect, useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { IPGroupRow } from "../lib/types";
import { Modal } from "./Modal";
import { Button } from "./ui";
import { CopyButton } from "./CopyButton";
import { useToast } from "./Toast";

type Path = "oauth" | "kimi" | "import";

// CPA waits this long for the callback after making a login link.
const OAUTH_WINDOW_MS = 5 * 60 * 1000;

const PATH_LABELS: Record<Path, string> = {
  oauth: "ChatGPT 登录",
  kimi: "Kimi Code（试用）",
  import: "导入授权",
};

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
  const [linkAt, setLinkAt] = useState(0); // when the login link was made
  const [linkDead, setLinkDead] = useState(false); // CPA gave up on this login
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!authUrl) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [authUrl]);
  const leftMs = linkAt ? linkAt + OAUTH_WINDOW_MS - now : OAUTH_WINDOW_MS;
  const expired = linkDead || leftMs <= 0;

  // kimi device-login state
  const [kimi, setKimi] = useState<{ url: string; state: string; code?: string } | null>(null);

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
    setLinkAt(0);
    setLinkDead(false);
    setKimi(null);
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
      setRedirectUrl("");
      setLinkAt(Date.now());
      setNow(Date.now());
      setLinkDead(false);
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
    onError: (e: unknown) => {
      // 410: this login is over, only a new link helps.
      if (e instanceof ApiError && e.status === 410) setLinkDead(true);
      toast(e instanceof Error ? e.message : "完成失败", "error");
    },
  });

  const startKimi = useMutation({
    mutationFn: () =>
      api.post<{ auth_url: string; state: string; user_code?: string }>(
        "/api/admin/accounts/kimi-start",
        { ip_group_id: Number(ipGroupId), os_profile: osProfile() }
      ),
    onSuccess: (d) => setKimi({ url: d.auth_url, state: d.state, code: d.user_code }),
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "启动失败", "error"),
  });

  const completeKimi = useMutation({
    mutationFn: () =>
      api.post<{ account_id: number }>("/api/admin/accounts/kimi-complete", {
        state: kimi?.state,
      }),
    onSuccess: () => {
      toast("Kimi 账号已添加", "success");
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

        {/* path tabs */}
        <div className="inline-flex rounded-lg border border-slate-300 bg-white p-0.5">
          {(["oauth", "kimi", "import"] as Path[]).map((p) => (
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
              {PATH_LABELS[p]}
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
                  <div className="flex flex-wrap items-center gap-2">
                    <CopyButton text={authUrl} label="复制登录链接" />
                    <span className="text-slate-500">粘到浏览器的无痕窗口里打开</span>
                    <span className={"ml-auto text-xs " + (expired ? "text-rose-600" : leftMs < 60_000 ? "text-amber-600" : "text-slate-400")}>
                      {expired
                        ? "链接已失效"
                        : `${Math.floor(leftMs / 60000)}:${String(Math.floor((leftMs % 60000) / 1000)).padStart(2, "0")} 内有效`}
                    </span>
                  </div>
                  <p className="mt-2 text-xs leading-relaxed text-slate-500">
                    用无痕窗口，避免登录成你平时浏览器里已登录的账号。登录完成后回调页（localhost:1455）打不开是正常的，复制地址栏的完整 URL 粘回下面。也可以直接
                    <a
                      href={authUrl}
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
                    placeholder="http://localhost:1455/auth/callback?code=..."
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
                    onClick={() => completeOAuth.mutate()}
                    disabled={!redirectUrl.trim() || expired || completeOAuth.isPending}
                  >
                    {completeOAuth.isPending ? "处理中…" : "完成"}
                  </Button>
                  <Button
                    variant={expired ? "primary" : "ghost"}
                    onClick={() => startOAuth.mutate()}
                    disabled={startOAuth.isPending}
                  >
                    {startOAuth.isPending ? "获取中…" : "重新获取登录链接"}
                  </Button>
                </div>
              </>
            )}
          </div>
        ) : path === "kimi" ? (
          <div className="space-y-3">
            <p className="text-xs leading-relaxed text-slate-500">
              用于上 ChatGPT 之前的试跑：Kimi Code 订阅经 CPA 接入，员工照常用 Codex。
              账号不会参与“疑似降智”检查，也没有 5 小时 / 每周额度显示。
            </p>
            {!kimi ? (
              <Button
                variant="primary"
                onClick={() => startKimi.mutate()}
                disabled={!canProceed || startKimi.isPending}
              >
                {startKimi.isPending ? "启动中…" : "开始 Kimi 登录"}
              </Button>
            ) : (
              <>
                <div className="rounded-lg bg-slate-50 px-4 py-3 text-sm text-slate-600">
                  <a
                    href={kimi.url}
                    target="_blank"
                    rel="noreferrer"
                    className="break-all font-medium text-indigo-600 hover:text-indigo-500"
                  >
                    打开 Kimi 授权页面并确认 ↗
                  </a>
                  {kimi.code && (
                    <p className="mt-2 text-xs text-slate-500">
                      如页面要求输入验证码：
                      <span className="ml-1 font-mono text-sm font-semibold text-slate-800">
                        {kimi.code}
                      </span>
                    </p>
                  )}
                  <p className="mt-2 text-xs leading-relaxed text-slate-500">
                    在页面里登录 Kimi 账号并点“授权”，回到这里点“我已授权，完成”。
                  </p>
                </div>
                <Button
                  variant="primary"
                  onClick={() => completeKimi.mutate()}
                  disabled={completeKimi.isPending}
                >
                  {completeKimi.isPending ? "确认中…" : "我已授权，完成"}
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
