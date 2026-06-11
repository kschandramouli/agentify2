import { useQuery } from "@tanstack/react-query";
import { getMetricsSummary, type MetricsSummary } from "../api";

function StatCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="adm-stat">
      <div className="adm-stat__value">{value}</div>
      <div className="adm-stat__label">{label}</div>
      {sub && <div className="adm-stat__sub">{sub}</div>}
    </div>
  );
}

function BreakdownRow({ label, value, total }: { label: string; value: number; total: number }) {
  const pct = total > 0 ? Math.round((value / total) * 100) : 0;
  return (
    <div className="adm-breakdown-row">
      <span className="adm-breakdown-row__label">{label}</span>
      <div className="adm-breakdown-row__bar-wrap">
        <div className="adm-breakdown-row__bar" style={{ width: `${pct}%` }} />
      </div>
      <span className="adm-breakdown-row__count">{value.toLocaleString()}</span>
      <span className="adm-breakdown-row__pct">{pct}%</span>
    </div>
  );
}

export function MetricsPanel() {
  const { data, isLoading, isError, error, dataUpdatedAt } = useQuery<MetricsSummary, Error>({
    queryKey: ["metrics-summary"],
    queryFn: getMetricsSummary,
    refetchInterval: 30_000,
  });

  const updatedAt = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : null;

  const tier1 = data?.queries_by_tier?.["tier1"] ?? 0;
  const tier2 = data?.queries_by_tier?.["tier2"] ?? 0;
  const total = data?.total_queries ?? 0;

  const intentEntries = Object.entries(data?.queries_by_intent ?? {})
    .sort((a, b) => b[1] - a[1]);

  const statusEntries = Object.entries(data?.queries_by_status ?? {})
    .sort((a, b) => b[1] - a[1]);

  return (
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Metrics</h2>
          <p className="adm-panel__desc">
            Query volume, tier split, and agent latency — derived from recorded traces.
          </p>
        </div>
        {updatedAt && <span className="adm-updated">Updated {updatedAt}</span>}
      </div>

      {isLoading && <p className="adm-loading">Loading…</p>}
      {isError && <p className="adm-error">{error.message}</p>}

      {data && (
        <>
          {/* Top stat cards */}
          <div className="adm-stats-row">
            <StatCard label="Total queries" value={total.toLocaleString()} />
            <StatCard label="Last 24 h" value={data.last_24h_count.toLocaleString()} />
            <StatCard label="Tier-1 (free)" value={tier1.toLocaleString()}
              sub={total > 0 ? `${Math.round((tier1/total)*100)}% of queries` : undefined} />
            <StatCard label="Tier-2 (LLM)" value={tier2.toLocaleString()}
              sub={total > 0 ? `${Math.round((tier2/total)*100)}% of queries` : undefined} />
            <StatCard label="Avg agent latency"
              value={data.avg_agent_latency_ms > 0 ? `${Math.round(data.avg_agent_latency_ms)}ms` : "—"} />
            <StatCard label="P95 agent latency"
              value={data.p95_agent_latency_ms > 0 ? `${Math.round(data.p95_agent_latency_ms)}ms` : "—"} />
          </div>

          <div className="adm-breakdowns">
            {/* By intent */}
            {intentEntries.length > 0 && (
              <div className="adm-breakdown">
                <h3>By intent</h3>
                {intentEntries.map(([k, v]) => (
                  <BreakdownRow key={k} label={k} value={v} total={total} />
                ))}
              </div>
            )}

            {/* By status */}
            {statusEntries.length > 0 && (
              <div className="adm-breakdown">
                <h3>By status</h3>
                {statusEntries.map(([k, v]) => (
                  <BreakdownRow key={k} label={k} value={v} total={total} />
                ))}
              </div>
            )}
          </div>

          {total === 0 && (
            <div className="adm-empty">No query data yet — run a query in K8s Observability first.</div>
          )}
        </>
      )}
    </div>
  );
}
