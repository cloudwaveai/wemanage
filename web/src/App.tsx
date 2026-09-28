import type React from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { useAuth } from "./context/AuthContext";
import { Layout } from "./components/Layout";
import { Login } from "./pages/Login";
import { KeyPage } from "./pages/KeyPage";
import { ConfigPage } from "./pages/ConfigPage";
import { UsagePage } from "./pages/UsagePage";
import { ReportsPage } from "./pages/ReportsPage";
import { LogsPage } from "./pages/LogsPage";
import { LogDetailPage } from "./pages/LogDetailPage";
import { OverviewPage } from "./pages/OverviewPage";
import { EmployeesPage } from "./pages/EmployeesPage";
import { AccountsPage } from "./pages/AccountsPage";
import { IPGroupsPage } from "./pages/IPGroupsPage";
import { AuditPage } from "./pages/AuditPage";
import { SettingsPage } from "./pages/SettingsPage";
import { DevicesPage } from "./pages/DevicesPage";
import { RiskPage } from "./pages/RiskPage";
import { RiskEmployeePage } from "./pages/RiskEmployeePage";
import { TeamPage } from "./pages/TeamPage";
import type { Role } from "./lib/types";

function FullScreen({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-50 text-sm text-slate-500">
      {children}
    </div>
  );
}

export default function App() {
  const { me, loading, error } = useAuth();

  if (loading) {
    return (
      <FullScreen>
        <span className="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-indigo-500" />
        加载中…
      </FullScreen>
    );
  }

  if (error) {
    return (
      <FullScreen>
        <div className="max-w-md rounded-lg border border-rose-200 bg-rose-50 px-6 py-4 text-rose-700">
          无法连接服务：
          {error instanceof Error ? error.message : String(error)}
        </div>
      </FullScreen>
    );
  }

  if (!me) return <Login />;

  const role: Role = me.role;
  const canOversee = role === "leader" || role === "super_admin";
  const isAdmin = role === "super_admin";

  return (
    <>
      <Layout>
        <Routes>
          <Route
            path="/"
            element={isAdmin ? <OverviewPage /> : <Navigate to="/key" replace />}
          />
          <Route path="/key" element={<KeyPage />} />
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/config" element={<ConfigPage />} />
          <Route path="/usage" element={<UsagePage />} />
          <Route path="/reports" element={<ReportsPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/logs/:id" element={<LogDetailPage />} />

          {canOversee && <Route path="/team" element={<TeamPage />} />}

          {isAdmin && <Route path="/employees" element={<EmployeesPage />} />}
          {isAdmin && <Route path="/risk" element={<RiskPage />} />}
          {isAdmin && (
            <Route path="/risk/employees/:id" element={<RiskEmployeePage />} />
          )}
          {isAdmin && <Route path="/accounts" element={<AccountsPage />} />}
          {isAdmin && <Route path="/ip-groups" element={<IPGroupsPage />} />}
          {isAdmin && <Route path="/audit" element={<AuditPage />} />}
          {isAdmin && <Route path="/settings" element={<SettingsPage />} />}

          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Layout>
    </>
  );
}
