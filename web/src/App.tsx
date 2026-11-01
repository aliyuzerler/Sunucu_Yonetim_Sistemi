import { useState } from "react";
import { Sidebar } from "./components/layout/Sidebar";
import { TopBar } from "./components/layout/TopBar";
import { OverviewPage } from "./pages/OverviewPage";
import { ComingSoonPage } from "./pages/ComingSoonPage";

export function App() {
  const [active, setActive] = useState("overview");
  return (
    <div className="flex h-screen bg-surface text-ink">
      <Sidebar active={active} onNavigate={setActive} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar />
        <main className="min-h-0 flex-1 overflow-y-auto" id="main-content">
          {active === "overview" ? <OverviewPage /> : <ComingSoonPage moduleId={active} />}
        </main>
      </div>
    </div>
  );
}
