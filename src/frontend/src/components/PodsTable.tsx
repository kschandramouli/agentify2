import { useQuery } from "@tanstack/react-query";
import { listPods, type Pod } from "../api";

export function PodsTable() {
  const { data, isLoading, isError, error } = useQuery<Pod[], Error>({
    queryKey: ["pods"],
    queryFn: listPods,
    refetchInterval: 10_000,
  });

  return (
    <section className="panel">
      <h2>Pods</h2>
      {isLoading && <p className="muted">Loading…</p>}
      {isError && <p className="error">Error: {error.message}</p>}
      {data && data.length === 0 && (
        <p className="muted">No pods yet — ingest an event to form one.</p>
      )}
      {data && data.length > 0 && (
        <table className="pods">
          <thead>
            <tr>
              <th>ID</th>
              <th>Kind</th>
              <th>Namespace</th>
              <th>Store</th>
              <th>Lifecycle</th>
              <th>Events</th>
            </tr>
          </thead>
          <tbody>
            {data.map((p) => (
              <tr key={p.id}>
                <td>
                  <code>{p.id}</code>
                </td>
                <td>{p.kind}</td>
                <td>{p.namespace}</td>
                <td>{p.store_type}</td>
                <td>{p.lifecycle}</td>
                <td>{p.event_count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
