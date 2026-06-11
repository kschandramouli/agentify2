import { useState } from "react";
import { ServiceEvaluator } from "./components/ServiceEvaluator";
import { IntegrationsPanel } from "./components/IntegrationsPanel";

type Tab = "evaluator" | "integrations";

export function App() {
  const [tab, setTab] = useState<Tab>("evaluator");

  return (
    <div className="app">
      <header className="app__header">
        <h1>agentify</h1>
        <span className="app__subtitle">Service Intelligence</span>
        <nav className="app__nav">
          <button
            className={`app__nav-btn${tab === "evaluator" ? " app__nav-btn--active" : ""}`}
            onClick={() => setTab("evaluator")}
          >
            Evaluator
          </button>
          <button
            className={`app__nav-btn${tab === "integrations" ? " app__nav-btn--active" : ""}`}
            onClick={() => setTab("integrations")}
          >
            Integrations
          </button>
        </nav>
      </header>
      <main className="app__main">
        {tab === "evaluator" && <ServiceEvaluator />}
        {tab === "integrations" && <IntegrationsPanel />}
      </main>
    </div>
  );
}
