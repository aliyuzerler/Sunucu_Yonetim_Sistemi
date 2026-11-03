import { Sidebar } from "./components/layout/Sidebar";
import { TopBar } from "./components/layout/TopBar";
import { useAuth } from "./auth/AuthProvider";
import { AuditPage } from "./pages/AuditPage";
import { BackupsPage } from "./pages/BackupsPage";
import { CloudPage } from "./pages/CloudPage";
import { ComingSoonPage } from "./pages/ComingSoonPage";
import { EnrollmentPage } from "./pages/EnrollmentPage";
import { ExtensionsPage } from "./pages/ExtensionsPage";
import { LoginPage } from "./pages/LoginPage";
import { DnsPage } from "./pages/DnsPage";
import { LogsPage } from "./pages/LogsPage";
import { MonitoringPage } from "./pages/MonitoringPage";
import { NodesPage } from "./pages/NodesPage";
import { SecurityPage } from "./pages/SecurityPage";
import { SslPage } from "./pages/SslPage";
import { OverviewPage } from "./pages/OverviewPage";
import { TasksPage } from "./pages/TasksPage";
import { UpdatesPage } from "./pages/UpdatesPage";
import { UsersPage } from "./pages/UsersPage";
import { ToastProvider } from "./components/ToastProvider";
import { useCallback, useEffect, useState } from "react";
import { parseHash, buildHash } from "./navigation/router";
import type { Route } from "./navigation/router";

// UX denetimi m.4.1: Gezinme artık useState yerine URL hash'inden türetilir.
// Geri/ileri tuşları, yer imi ve sayfa yenileme çalışır; her görünüm
// paylaşılabilir bir adres'e sahiptir (#/nodes, #/tasks, …).
const KNOWN_PAGES = [
  "overview",
  "nodes",
  "tasks",
  "audit",
  "users",
  "enrollment",
  "dns",
  "ssl",
  "monitoring",
  "logs",
  "security",
  "backups",
  "integrations",
  "modules",
  "updates",
];

export function App() {
  const { status, user } = useAuth();
  const [route, setRoute] = useState<Route>(() => parseHash(window.location.hash));

  useEffect(() => {
    const onHashChange = () => setRoute(parseHash(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const navigate = useCallback((page: string, sub?: string | null) => {
    const target = buildHash(page, sub);
    const parsed = parseHash(target);
    // İyimser senkron güncelleme: tıklama → görünüm aynı commit'te olur
    // (hashchange event'i bir makro-görev sonra gelir; UI gecikmesin).
    // hashchange dinleyicisi aynı değeri tekrar bildirir — zararsız.
    setRoute(parsed);
    if (window.location.hash !== target) {
      window.location.hash = target;
    }
  }, []);

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
    <ToastProvider>
      <div className="flex h-screen bg-surface text-ink">
        <Sidebar active={route.page} onNavigate={navigate} />
        <div className="flex min-w-0 flex-1 flex-col">
          <TopBar />
          <main className="min-h-0 flex-1 overflow-y-auto" id="main-content">
            {route.page === "overview" && <OverviewPage />}
            {/* m.4.2: #/nodes/<id> derin bağlantısı doğrudan detay görünümünü açar. */}
            {route.page === "nodes" && (
              <NodesPage routeNodeId={route.nodeId} onOpenNode={(id) => navigate("nodes", id)} onNodeClosed={() => navigate("nodes")} />
            )}
            {route.page === "tasks" && <TasksPage />}
            {route.page === "audit" && <AuditPage />}
            {route.page === "users" && <UsersPage />}
            {route.page === "enrollment" && <EnrollmentPage />}
            {route.page === "dns" && <DnsPage />}
            {route.page === "ssl" && <SslPage />}
            {/* Faz 13: izleme, log arama ve güvenlik merkezi. */}
            {route.page === "monitoring" && <MonitoringPage />}
            {route.page === "logs" && <LogsPage />}
            {route.page === "security" && <SecurityPage />}
            {/* Faz 14: yedekleme ve bulut sağlayıcı entegrasyonları. */}
            {route.page === "backups" && <BackupsPage />}
            {route.page === "integrations" && <CloudPage />}
            {/* Faz 15: eklenti sözleşmesi ve imzalı güncellemeler. */}
            {route.page === "modules" && <ExtensionsPage />}
            {route.page === "updates" && <UpdatesPage />}
            {!KNOWN_PAGES.includes(route.page) && <ComingSoonPage moduleId={route.page} />}
          </main>
        </div>
      </div>
    </ToastProvider>
  );
}
