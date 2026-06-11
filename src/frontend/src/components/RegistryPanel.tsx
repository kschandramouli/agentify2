import { useQuery } from "@tanstack/react-query";
import { listPods, type Pod } from "../api";

const LIFECYCLE_COLOR: Record<string, string> = {
  active:   "adm-badge adm-badge--ok",
  draining: "adm-badge adm-badge--warn",
  retired:  "adm-badge adm-badge--muted",
};

export function RegistryPanel() {
  const { data, isLoading, isError, error, dataUpdatedAt } = useQuery<Pod[], Error>({
    queryKey: ["pods"],
    queryFn: listPods,
    refetchInterval: 10_000,
  });

  const updatedAt = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : null;

  return (
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Pod Registry</h2>
          <p className="adm-panel__desc">
            Every context-mesh pod the system is tracking — store type, lifecycle, and event count.
          </p>
        </div>
        {updatedAt && <span className="adm-updated">Updated {updatedAt}</span>}
      </div>

      {isLoading && <p className="adm-loading">Loading…</p>}
      {isError && <p className="adm-error">{error.message}</p>}

      {data && data.length === 0 && (
        <div className="adm-empty">No pods yet — ingest an event to form one.</div>
      )}

      {data && data.length > 0 && (
        <div className="adm-table-wrap">
          <table className="adm-table">
            <thead>
              <tr>
                <th>Pod ID</th>
                <th>Namespace</th>
                <th>Kind</th>
                <th>Store</th>
                <th>Lifecycle</th>
                <th>Events</th>
                <th>Freshness</th>
              </tr>
            </thead>
            <tbody>
              {data.map(p => (
                <tr key={p.id}>
                  <td><code className="adm-code">{p.id}</code></td>
                  <td>{p.namespace}</td>
                  <td>{p.kind}</td>
                  <td><span className="adm-badge adm-badge--muted">{p.store_type}</span></td>
                  <td>
                    <span className={LIFECYCLE_COLOR[p.lifecycle] ?? "adm-badge adm-badge--muted"}>
                      {p.lifecycle}
                    </span>
                  </td>
                  <td className="adm-num">{p.event_count.toLocaleString()}</td>
                  <td className="adm-muted">{p.freshness ? new Date(p.freshness).toLocaleTimeString() : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
