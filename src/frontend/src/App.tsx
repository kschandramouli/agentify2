import { useState } from "react";
import { ServiceEvaluator } from "./components/ServiceEvaluator";

type Page = "observability";

function Sidebar({ page, onNavigate }: { page: Page; onNavigate: (p: Page) => void }) {
  return (
    <nav className="sidebar">
      <button
        className={`sidebar__item${page === "observability" ? " sidebar__item--active" : ""}`}
        onClick={() => onNavigate("observability")}
      >
        <span className="sidebar__icon">⬡</span>
        K8s Observability
      </button>
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
        </main>
      </div>
    </div>
  );
}
