import { ServiceEvaluator } from "./components/ServiceEvaluator";
import { PodsTable } from "./components/PodsTable";

export function App() {
  return (
    <div className="app">
      <header className="app__header">
        <h1>agentify</h1>
        <span className="app__subtitle">Service Intelligence</span>
      </header>
      <main className="app__main">
        <ServiceEvaluator />
        <PodsTable />
      </main>
    </div>
  );
}
