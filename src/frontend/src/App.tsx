import { useState } from "react";
import { ServiceEvaluator } from "./components/ServiceEvaluator";
import { IntegrationsPanel } from "./components/IntegrationsPanel";

type Page = "observability" | "integrations";

function Sidebar({ page, onNavigate }: { page: Page; onNavigate: (p: Page) => void }) {
  const [adminOpen, setAdminOpen] = useState(true);

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
          aria-expanded={adminOpen}
        >
          <span className="sidebar__section-arrow">{adminOpen ? "▾" : "▸"}</span>
          Admin
        </button>
        {adminOpen && (
          <div className="sidebar__children">
            <button
              className={`sidebar__item sidebar__item--child${page === "integrations" ? " sidebar__item--active" : ""}`}
              onClick={() => onNavigate("integrations")}
            >
              <span className="sidebar__icon">⇄</span>
              Integrations
            </button>
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
          {page === "integrations" && <IntegrationsPanel />}
        </main>
      </div>
    </div>
  );
}
