import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listTraces, type TraceRecord } from "../api";

const TIER_CLS: Record<string, string> = {
  tier1:   "adm-badge adm-badge--tier1",
  tier2:   "adm-badge adm-badge--tier2",
  none:    "adm-badge adm-badge--muted",
  no_data: "adm-badge adm-badge--muted",
};

const STATUS_CLS: Record<string, string> = {
  ok:      "adm-badge adm-badge--ok",
  partial: "adm-badge adm-badge--warn",
  error:   "adm-badge adm-badge--crit",
  no_data: "adm-badge adm-badge--muted",
};

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return `${Math.round(diff / 1000)}s ago`;
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h ago`;
  return new Date(iso).toLocaleDateString();
}

function truncate(s: string, n: number) {
  return s.length > n ? s.slice(0, n) + "…" : s;
}

export function TracesPanel() {
  const [intentFilter, setIntentFilter] = useState("");
  const [tierFilter, setTierFilter]     = useState("");

  const { data = [], isLoading, isError, error } = useQuery<TraceRecord[], Error>({
    queryKey: ["traces"],
    queryFn: listTraces,
    refetchInterval: 15_000,
  });

  const intents = [...new Set(data.map(t => t.intent).filter(Boolean))].sort();
  const tiers   = [...new Set(data.map(t => t.tier).filter(Boolean))].sort();

  const rows = data.filter(t =>
    (!intentFilter || t.intent === intentFilter) &&
    (!tierFilter   || t.tier   === tierFilter)
  );

  return (
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Query History</h2>
          <p className="adm-panel__desc">
            Last 200 queries — intent, tier, status, confidence, and latency.
          </p>
        </div>
        <div className="adm-filters">
          <select value={intentFilter} onChange={e => setIntentFilter(e.target.value)}>
            <option value="">All intents</option>
            {intents.map(i => <option key={i} value={i}>{i}</option>)}
          </select>
          <select value={tierFilter} onChange={e => setTierFilter(e.target.value)}>
            <option value="">All tiers</option>
            {tiers.map(t => <option key={t} value={t}>{t}</option>)}
          </select>
        </div>
      </div>

      {isLoading && <p className="adm-loading">Loading…</p>}
      {isError && <p className="adm-error">{error.message}</p>}

      {!isLoading && rows.length === 0 && (
        <div className="adm-empty">No queries recorded yet.</div>
      )}

      {rows.length > 0 && (
        <div className="adm-table-wrap">
          <table className="adm-table adm-table--traces">
            <thead>
              <tr>
                <th>When</th>
                <th>Question</th>
                <th>Intent</th>
                <th>Namespace</th>
                <th>Tier</th>
                <th>Status</th>
                <th>Conf</th>
                <th>Latency</th>
                <th>Tools</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(t => (
                <tr key={t.id}>
                  <td className="adm-muted adm-nowrap">{relativeTime(t.created_at)}</td>
                  <td className="adm-question" title={t.question}>{truncate(t.question, 60)}</td>
                  <td><code className="adm-code">{t.intent}</code></td>
                  <td className="adm-muted">{t.namespace || "—"}</td>
                  <td><span className={TIER_CLS[t.tier] ?? "adm-badge adm-badge--muted"}>{t.tier}</span></td>
                  <td><span className={STATUS_CLS[t.status] ?? "adm-badge adm-badge--muted"}>{t.status}</span></td>
                  <td className="adm-num">{t.confidence ? `${Math.round(t.confidence * 100)}%` : "—"}</td>
                  <td className="adm-num adm-nowrap">{t.latency_ms > 0 ? `${t.latency_ms}ms` : "—"}</td>
                  <td className="adm-muted">{t.tool_calls?.join(", ") || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
