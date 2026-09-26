import React, { useState } from "react";
import { useAuth } from "../context/AuthContext";
import { api, ApiError, setCsrfToken } from "../lib/api";

export function Login() {
  const { refetch } = useAuth();
  const [devVisible, setDevVisible] = useState(true);
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
          <a
            href="/api/auth/feishu/login"
            className="flex w-full items-center justify-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-indigo-500"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
              <path d="M4 5h16v14H4z" opacity="0" />
              <path d="M6 4h9l3 3v13H6z" opacity="0" />
            </svg>
            飞书登录
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
