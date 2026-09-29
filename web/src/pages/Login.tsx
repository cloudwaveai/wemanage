import React, { useEffect, useState } from "react";
import { useAuth } from "../context/AuthContext";
import { api, ApiError, setCsrfToken } from "../lib/api";

// Opened inside the Feishu client (workbench, or a link in a bot message):
// the user is already signed in to Feishu, so log in without a click.
const IN_FEISHU = /Lark|Feishu/i.test(navigator.userAgent);
const LOGGED_OUT_FLAG = "aiseat_logged_out";

function loggedOutThisTab(): boolean {
  try {
    return sessionStorage.getItem(LOGGED_OUT_FLAG) === "1";
  } catch {
    return false;
  }
}

export function Login() {
  const { refetch } = useAuth();
  const params = new URLSearchParams(window.location.search);
  const loginError = params.get("login_error");
  params.delete("login_error");
  const back = window.location.pathname + (params.toString() ? "?" + params : "");
  const loginUrl = "/api/auth/feishu/login?next=" + encodeURIComponent(back);
  const auto = IN_FEISHU && !loginError && !loggedOutThisTab();
  useEffect(() => {
    if (auto) window.location.replace(loginUrl);
  }, [auto, loginUrl]);
  useEffect(() => {
    api
      .get<{ dev_login: boolean }>("/api/auth/options")
      .then((o) => setDevVisible(o.dev_login))
      .catch(() => setDevVisible(false));
  }, []);

  const [devVisible, setDevVisible] = useState(false);
  const [openId, setOpenId] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const devLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!openId.trim()) {
      setErr("请填写 open_id");
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const resp = await api.post<{ csrf: string }>("/api/auth/dev", {
        open_id: openId.trim(),
        name: name.trim(),
      });
      setCsrfToken(resp.csrf);
      refetch();
    } catch (e2) {
      if (e2 instanceof ApiError && e2.status === 404) {
        // dev login is disabled on this deployment — hide the form.
        setDevVisible(false);
      } else {
        setErr(e2 instanceof Error ? e2.message : "登录失败");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-b from-slate-50 to-indigo-50 px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-xl bg-indigo-600 text-lg font-bold text-white">
            AI
          </div>
          <h1 className="text-xl font-semibold text-slate-900">AI 订阅网关</h1>
          <p className="mt-1 text-sm text-slate-500">管理与自助门户</p>
        </div>

        <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          {loginError && (
            <div className="mb-4 rounded-lg bg-rose-50 px-4 py-3 text-sm text-rose-700">
              {loginError}
            </div>
          )}
          <a
            href={loginUrl}
            className="flex w-full items-center justify-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-indigo-500"
          >
            {auto ? "正在用飞书登录…" : "飞书登录"}
          </a>

          {devVisible && (
            <>
              <div className="my-4 flex items-center gap-3 text-xs text-slate-400">
                <span className="h-px flex-1 bg-slate-200" />
                开发登录
                <span className="h-px flex-1 bg-slate-200" />
              </div>
              <form onSubmit={devLogin} className="space-y-3">
                <input
                  value={openId}
                  onChange={(e) => setOpenId(e.target.value)}
                  placeholder="open_id"
                  className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
                />
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="姓名（可选）"
                  className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100"
                />
                {err && <div className="text-xs text-rose-600">{err}</div>}
                <button
                  type="submit"
                  disabled={busy}
                  className="w-full rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
                >
                  {busy ? "登录中…" : "开发登录"}
                </button>
              </form>
              <p className="mt-3 text-center text-[11px] text-slate-400">
                开发登录仅用于本地环境
              </p>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
