import React, { useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { UpdateBanner } from "./UpdateBanner";
import { Modal } from "./Modal";
import { GettingStartedBody } from "./GettingStarted";
import { useMutation } from "@tanstack/react-query";
import { useAuth } from "../context/AuthContext";
import { useToast } from "./Toast";
import { api } from "../lib/api";
import type { Role } from "../lib/types";

interface NavItem {
  to: string;
  label: string;
  icon: JSX.Element;
  roles: Role[];
  end?: boolean;
  reloadDocument?: boolean;
}

const I = (path: string) => (
  <svg
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d={path} />
  </svg>
);

const PERSONAL: NavItem[] = [
  {
    to: "/key",
    label: "我的 Key",
    roles: ["employee", "leader", "super_admin"],
    icon: I("M15 7a4 4 0 11-4 4m4-4l6 6m-3-3l-2 2M7 11a4 4 0 108 0 4 4 0 00-8 0z"),
  },
  {
    to: "/devices",
    label: "我的设备",
    roles: ["employee", "leader", "super_admin"],
    icon: I("M4 5h16a1 1 0 011 1v9a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1zM8 20h8M12 16v4"),
  },
  {
    to: "/usage",
    label: "我的用量",
    roles: ["employee", "leader", "super_admin"],
    icon: I("M4 19V5m0 14h16M8 15v-4m4 4V8m4 7v-2"),
  },
  {
    to: "/logs",
    label: "使用日志",
    roles: ["employee", "leader"],
    icon: I("M21 21l-4.3-4.3M11 18a7 7 0 100-14 7 7 0 000 14z"),
  },
  {
    to: "/reports",
    label: "我的周报",
    roles: ["employee", "leader", "super_admin"],
    icon: I("M8 3v4M16 3v4M4 9h16M5 5h14a1 1 0 011 1v13a1 1 0 01-1 1H5a1 1 0 01-1-1V6a1 1 0 011-1z"),
  },
];

const OVERSIGHT: NavItem[] = [
  {
    to: "/team",
    label: "团队",
    roles: ["leader", "super_admin"],
    icon: I("M9 11a3 3 0 100-6 3 3 0 000 6zM16 11a3 3 0 100-6M3 20v-1a4 4 0 014-4h4a4 4 0 014 4v1M17 15h1a4 4 0 014 4v1"),
  },
];

const ADMIN: NavItem[] = [
  {
    to: "/",
    label: "总览",
    roles: ["super_admin"],
    end: true,
    icon: I("M3 12l9-9 9 9M5 10v10h14V10"),
  },
  {
    to: "/wemanager/", label: "We-Manage", roles: ["super_admin"], reloadDocument: true,
    icon: I("M4 18V7m0 11h16M8 14l3-3 3 2 4-6"),
  },
  {
    to: "/workboard",
    label: "工作看板",
    roles: ["super_admin"],
    icon: I("M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 012-2h2a2 2 0 012 2v0a2 2 0 01-2 2h-2a2 2 0 01-2-2zM9 12h6M9 16h4"),
  },
  {
    to: "/briefing",
    label: "简报",
    roles: ["super_admin"],
    icon: I("M7 3h7l5 5v12a1 1 0 01-1 1H7a1 1 0 01-1-1V4a1 1 0 011-1zM14 3v5h5M9 13h6M9 17h4"),
  },
  {
    to: "/logs",
    label: "对话记录",
    roles: ["super_admin"],
    icon: I("M21 21l-4.3-4.3M11 18a7 7 0 100-14 7 7 0 000 14z"),
  },
  {
    to: "/employees",
    label: "员工",
    roles: ["super_admin"],
    icon: I("M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-1.13a4 4 0 10-4-4 4 4 0 004 4z"),
  },
  {
    to: "/risk",
    label: "风险中心",
    roles: ["super_admin"],
    icon: I("M12 3l8 3v6c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V6l8-3zM12 8v5M12 16h.01"),
  },
  {
    to: "/accounts",
    label: "账号",
    roles: ["super_admin"],
    icon: I("M4 7h16M4 7a2 2 0 012-2h12a2 2 0 012 2M4 7v10a2 2 0 002 2h12a2 2 0 002-2V7M9 12h6"),
  },
  {
    to: "/projects", label: "项目与用量", roles: ["super_admin"],
    icon: I("M3 6h7l2 2h9v11H3z"),
  },
  {
    to: "/api-channels",
    label: "API 渠道",
    roles: ["super_admin"],
    icon: I("M4 8h16M4 16h16M8 5v6M16 13v6"),
  },
  {
    to: "/ip-groups",
    label: "IP 组",
    roles: ["super_admin"],
    icon: I("M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c2.5 2.5 3.5 6 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-6-3.5-9s1-6.5 3.5-9z"),
  },
  {
    to: "/audit",
    label: "审计",
    roles: ["super_admin"],
    icon: I("M9 12l2 2 4-4m1-6H8a2 2 0 00-2 2v14l6-3 6 3V6a2 2 0 00-2-2z"),
  },
  {
    to: "/settings",
    label: "设置",
    roles: ["super_admin"],
    icon: I("M10.3 3.5a1 1 0 011.4 0l1 1a1 1 0 00.9.3l1.4-.2a1 1 0 011.1.7l.5 1.3a1 1 0 00.6.6l1.3.5a1 1 0 01.7 1.1l-.2 1.4a1 1 0 00.3.9l1 1a1 1 0 010 1.4l-1 1a1 1 0 00-.3.9l.2 1.4a1 1 0 01-.7 1.1l-1.3.5a1 1 0 00-.6.6l-.5 1.3a1 1 0 01-1.1.7l-1.4-.2a1 1 0 00-.9.3l-1 1a1 1 0 01-1.4 0l-1-1a1 1 0 00-.9-.3l-1.4.2a1 1 0 01-1.1-.7l-.5-1.3a1 1 0 00-.6-.6l-1.3-.5a1 1 0 01-.7-1.1l.2-1.4a1 1 0 00-.3-.9l-1-1a1 1 0 010-1.4l1-1a1 1 0 00.3-.9l-.2-1.4a1 1 0 01.7-1.1l1.3-.5a1 1 0 00.6-.6l.5-1.3a1 1 0 011.1-.7l1.4.2a1 1 0 00.9-.3zM12 15a3 3 0 100-6 3 3 0 000 6z"),
  },
];

function roleLabel(role: Role): string {
  return role === "super_admin"
    ? "超级管理员"
    : role === "leader"
    ? "团队负责人"
    : "员工";
}

function NavSection({
  title,
  items,
  role,
  onNavigate,
  counts = {},
}: {
  title: string;
  items: NavItem[];
  role: Role;
  onNavigate: () => void;
  counts?: Record<string, number>;
}) {
  const visible = items.filter((i) => i.roles.includes(role));
  if (!visible.length) return null;
  return (
    <div className="mb-4">
      <div className="mb-1 px-3 text-[11px] font-semibold uppercase tracking-wider text-slate-400">
        {title}
      </div>
      <nav className="space-y-0.5">
        {visible.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            reloadDocument={item.reloadDocument}
            onClick={onNavigate}
            className={({ isActive }) =>
              "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors " +
              (isActive
                ? "bg-indigo-50 text-indigo-700"
                : "text-slate-600 hover:bg-slate-100")
            }
          >
            <span className="text-slate-400">{item.icon}</span>
            {item.label}
            {(counts[item.to] ?? 0) > 0 && (
              <span className="ml-auto rounded-full bg-amber-500 px-1.5 py-px text-[10px] font-semibold leading-4 text-white">
                {counts[item.to]}
              </span>
            )}
          </NavLink>
        ))}
      </nav>
    </div>
  );
}

// Account-level warnings shown above every page: a frozen key wins over
// devices awaiting confirmation, which win over the office-network notice.
function RiskBanner() {
  const { me } = useAuth();
  const { pathname } = useLocation();
  if (!me) return null;

  if (me.frozen) {
    return (
      <div className="mb-5 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
        你的 Key 已被冻结：{me.frozen_reason || "未说明原因"}
        。这通常表示 Key 可能被他人使用，需要管理员核实后解冻。
      </div>
    );
  }

  const n = me.pending_devices ?? 0;
  if (n > 0) {
    return (
      <div className="mb-5 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
        <span>有 {n} 台设备等待你确认，确认前可能无法使用。</span>
        {pathname !== "/devices" && (
          <Link
            to="/devices"
            className="font-medium text-amber-900 underline underline-offset-2 hover:text-amber-700"
          >
            去确认
          </Link>
        )}
      </div>
    );
  }

  if (me.office_only && !me.remote_allowed) {
    return (
      <div className="mb-5 rounded-lg border border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-800">
        你的 Key 只能在公司网络内使用。
      </div>
    );
  }
  return null;
}

export function Layout({ children }: { children: React.ReactNode }) {
  const { me, logout } = useAuth();
  const { toast } = useToast();
  const navigate = useNavigate();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const role = me?.role ?? "employee";

  const sync = useMutation({
    mutationFn: () =>
      api.post<{ departments: number; users: number }>("/api/admin/sync-feishu"),
    onSuccess: (d) =>
      toast(`飞书同步完成：部门 ${d.departments}，用户 ${d.users}`, "success"),
    onError: (e: unknown) =>
      toast(e instanceof Error ? e.message : "同步失败", "error"),
  });

  const closeMobile = () => setMobileOpen(false);

  const sidebar = (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 px-4 py-4">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-sm font-bold text-white">
          AI
        </div>
        <div className="leading-tight">
          <div className="text-sm font-semibold text-slate-900">AI 订阅网关</div>
          <div className="text-[11px] text-slate-400">管理与自助门户</div>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto px-2 py-2">
        <NavSection
          title="个人"
          items={PERSONAL}
          role={role}
          onNavigate={closeMobile}
          counts={{ "/devices": me?.pending_devices ?? 0 }}
        />
        <NavSection
          title="监管"
          items={OVERSIGHT}
          role={role}
          onNavigate={closeMobile}
        />
        <NavSection title="管理" items={ADMIN} role={role} onNavigate={closeMobile} />
      </div>
    </div>
  );

  return (
    <div className="flex h-full min-h-screen bg-slate-50">
      {/* desktop sidebar */}
      <aside className="hidden w-60 shrink-0 border-r border-slate-200 bg-white lg:block">
        {sidebar}
      </aside>

      {/* mobile drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div
            className="absolute inset-0 bg-slate-900/40"
            onClick={closeMobile}
          />
          <aside className="absolute left-0 top-0 h-full w-64 border-r border-slate-200 bg-white shadow-xl">
            {sidebar}
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <UpdateBanner />
        <header className="sticky top-0 z-30 flex items-center justify-between border-b border-slate-200 bg-white/90 px-4 py-3 backdrop-blur">
          <div className="flex items-center gap-2">
            <button
              className="rounded-md p-1.5 text-slate-500 hover:bg-slate-100 lg:hidden"
              onClick={() => setMobileOpen(true)}
              aria-label="菜单"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M4 6h16M4 12h16M4 18h16" strokeLinecap="round" />
              </svg>
            </button>
          </div>
          <div className="flex items-center gap-3">
            <button
              onClick={() => setGuideOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-slate-500 hover:bg-slate-100 hover:text-slate-700"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M12 21a9 9 0 100-18 9 9 0 000 18zM9.5 9a2.5 2.5 0 114 2c-.9.6-1.5 1.1-1.5 2.2M12 17h.01" />
              </svg>
              <span className="hidden sm:inline">使用指引</span>
            </button>
            {role === "super_admin" && (
              <button
                onClick={() => sync.mutate()}
                disabled={sync.isPending}
                className="hidden items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-600 hover:bg-slate-50 disabled:opacity-50 sm:inline-flex"
              >
                {sync.isPending ? "同步中…" : "同步飞书"}
              </button>
            )}
            <div className="flex items-center gap-2">
              {me?.avatar_url ? (
                <img
                  src={me.avatar_url}
                  alt=""
                  className="h-8 w-8 rounded-full object-cover"
                />
              ) : (
                <div className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-200 text-sm font-medium text-slate-600">
                  {me?.name?.[0] ?? "?"}
                </div>
              )}
              <div className="hidden leading-tight sm:block">
                <div className="text-sm font-medium text-slate-800">
                  {me?.name}
                </div>
                <div className="text-[11px] text-slate-400">
                  {roleLabel(role)}
                </div>
              </div>
            </div>
            <button
              onClick={async () => {
                await logout();
                navigate("/");
              }}
              className="rounded-lg px-2.5 py-1.5 text-sm font-medium text-slate-500 hover:bg-slate-100 hover:text-slate-700"
            >
              退出
            </button>
          </div>
        </header>

        <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:px-6">
          <RiskBanner />
          {children}
        </main>
      </div>

      <Modal open={guideOpen} onClose={() => setGuideOpen(false)} title="使用指引" width="max-w-2xl">
        <GettingStartedBody onNavigate={() => setGuideOpen(false)} />
      </Modal>
    </div>
  );
}
