import { Sidebar } from "./components/layout/Sidebar";
import { TopBar } from "./components/layout/TopBar";
import { useAuth } from "./auth/AuthProvider";
import { AuditPage } from "./pages/AuditPage";
import { ComingSoonPage } from "./pages/ComingSoonPage";
import { EnrollmentPage } from "./pages/EnrollmentPage";
import { LoginPage } from "./pages/LoginPage";
import { NodesPage } from "./pages/NodesPage";
import { OverviewPage } from "./pages/OverviewPage";
import { TasksPage } from "./pages/TasksPage";
import { UsersPage } from "./pages/UsersPage";
import { useState } from "react";

export function App() {
  const { status, user } = useAuth();
  const [active, setActive] = useState("overview");

  if (status === "loading") {
    return (
      <div className="flex h-screen items-center justify-center bg-surface text-sm text-ink-soft">
        <p role="status">…</p>
      </div>
    );
  }

  if (status === "anonymous" || !user) {
    return <LoginPage />;
  }

  return (
    <div className="flex h-screen bg-surface text-ink">
      <Sidebar active={active} onNavigate={setActive} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar />
        <main className="min-h-0 flex-1 overflow-y-auto" id="main-content">
          {active === "overview" && <OverviewPage />}
          {active === "nodes" && <NodesPage />}
          {active === "tasks" && <TasksPage />}
          {active === "audit" && <AuditPage />}
          {active === "users" && <UsersPage />}
          {active === "enrollment" && <EnrollmentPage />}
          {!["overview", "nodes", "tasks", "audit", "users", "enrollment"].includes(active) && (
            <ComingSoonPage moduleId={active} />
          )}
        </main>
      </div>
    </div>
  );
}
