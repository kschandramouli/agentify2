import { useState } from "react";
import { ServiceEvaluator } from "./components/ServiceEvaluator";
import { RegistryPanel } from "./components/RegistryPanel";
import { TracesPanel } from "./components/TracesPanel";
import { SyncPanel } from "./components/SyncPanel";
import { MetricsPanel } from "./components/MetricsPanel";

type Page = "observability" | "registry" | "traces" | "sync" | "metrics";

const ADMIN_PAGES: { id: Page; label: string }[] = [
  { id: "registry", label: "Pod Registry" },
  { id: "traces",   label: "Query History" },
  { id: "sync",     label: "Namespace Sync" },
  { id: "metrics",  label: "Metrics" },
];

function Sidebar({ page, onNavigate }: { page: Page; onNavigate: (p: Page) => void }) {
  const [adminOpen, setAdminOpen] = useState<boolean>(
    ADMIN_PAGES.some(p => p.id === page)
  );

  return (
    <nav className="sidebar">
      <button
        className={`sidebar__item${page === "observability" ? " sidebar__item--active" : ""}`}
        onClick={() => onNavigate("observability")}
      >
        <span className="sidebar__icon">⬡</span>
        K8s Observability
      </button>

      <div className="sidebar__section">
        <button
          className="sidebar__section-header"
          onClick={() => setAdminOpen(o => !o)}
        >
          <span className="sidebar__section-arrow">{adminOpen ? "▾" : "▸"}</span>
          Admin
        </button>
        {adminOpen && (
          <div className="sidebar__children">
            {ADMIN_PAGES.map(({ id, label }) => (
              <button
                key={id}
                className={`sidebar__item sidebar__item--child${page === id ? " sidebar__item--active" : ""}`}
                onClick={() => onNavigate(id)}
              >
                {label}
              </button>
            ))}
          </div>
        )}
      </div>
    </nav>
  );
}

export function App() {
  const [page, setPage] = useState<Page>("observability");

  return (
    <div className="app">
      <header className="app__header">
        <h1>agentify</h1>
        <span className="app__subtitle">Service Intelligence</span>
      </header>
      <div className="app__body">
        <Sidebar page={page} onNavigate={setPage} />
        <main className="app__content">
          {page === "observability" && <ServiceEvaluator />}
          {page === "registry"      && <RegistryPanel />}
          {page === "traces"        && <TracesPanel />}
          {page === "sync"          && <SyncPanel />}
          {page === "metrics"       && <MetricsPanel />}
        </main>
      </div>
    </div>
  );
}
