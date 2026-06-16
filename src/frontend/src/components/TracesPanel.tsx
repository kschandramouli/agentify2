import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listTraces, getTrace, type TraceRecord } from "../api";

const TIER_CLS: Record<string, string> = {
  tier1:   "adm-badge adm-badge--tier1",
  tier2:   "adm-badge adm-badge--tier2",
  none:    "adm-badge adm-badge--muted",
  no_data: "adm-badge adm-badge--muted",
};

const STATUS_CLS: Record<string, string> = {
  ok:        "adm-badge adm-badge--ok",
  healthy:   "adm-badge adm-badge--ok",
  degraded:  "adm-badge adm-badge--warn",
  partial:   "adm-badge adm-badge--warn",
  unhealthy: "adm-badge adm-badge--crit",
  error:     "adm-badge adm-badge--crit",
  no_data:   "adm-badge adm-badge--muted",
};

function absTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short", day: "numeric",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  });
}

function relTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return `${Math.round(diff / 1_000)}s ago`;
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h ago`;
  return new Date(iso).toLocaleDateString();
}

function fmtLatency(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 1_000) return `${ms}ms`;
  return `${(ms / 1_000).toFixed(1)}s`;
}

function fmtCost(usd: number): string {
  if (!usd || usd === 0) return "—";
  if (usd < 0.0001) return `< $0.0001`;
  return `$${usd.toFixed(4)}`;
}

function fmtTokens(input: number, output: number): string {
  if (!input && !output) return "—";
  const fmt = (n: number) => n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`;
  return `${fmt(input)} / ${fmt(output)}`;
}

function fmtCacheTokens(write: number, read: number): string | null {
  if (!write && !read) return null;
  const fmt = (n: number) => n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`;
  const parts: string[] = [];
  if (write) parts.push(`${fmt(write)} wrote`);
  if (read)  parts.push(`${fmt(read)} read`);
  return parts.join(" · ");
}

function truncate(s: string, n: number) {
  return s.length > n ? s.slice(0, n) + "…" : s;
}

// Compute totals for Tier-2 calls visible in the current filtered view.
function computeTotals(rows: TraceRecord[]) {
  const tier2 = rows.filter(r => r.tier === "tier2");
  return {
    count:       tier2.length,
    cost:        tier2.reduce((s, r) => s + (r.estimated_cost_usd ?? 0), 0),
    inputTokens: tier2.reduce((s, r) => s + (r.input_tokens ?? 0), 0),
    outputTokens:tier2.reduce((s, r) => s + (r.output_tokens ?? 0), 0),
    cacheWrite:  tier2.reduce((s, r) => s + (r.cache_creation_input_tokens ?? 0), 0),
    cacheRead:   tier2.reduce((s, r) => s + (r.cache_read_input_tokens ?? 0), 0),
  };
}

// Drawer showing full detail for a single trace (GET /admin/traces/{id})
function TraceDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const { data, isLoading, isError } = useQuery<TraceRecord>({
    queryKey: ["trace", id],
    queryFn: () => getTrace(id),
  });

  return (
    <div className="trace-drawer" onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
      <div className="trace-drawer__panel">
        <div className="trace-drawer__header">
          <span className="trace-drawer__title">Trace detail</span>
          <button className="trace-drawer__close" onClick={onClose}>✕</button>
        </div>
        {isLoading && <p className="adm-loading">Loading…</p>}
        {isError   && <p className="adm-error">Failed to load trace.</p>}
        {data && (
          <div className="trace-drawer__body">
            <div className="trace-detail-grid">
              <span className="trace-detail-label">Question</span>
              <span className="trace-detail-value">{data.question}</span>

              <span className="trace-detail-label">Trace ID</span>
              <code className="trace-detail-value adm-code">{data.trace_id}</code>

              <span className="trace-detail-label">Intent</span>
              <code className="trace-detail-value adm-code">{data.intent}</code>

              <span className="trace-detail-label">Tier / Status</span>
              <span className="trace-detail-value">{data.tier} · {data.status}</span>

              <span className="trace-detail-label">Namespace</span>
              <span className="trace-detail-value">{data.namespace || "—"}</span>

              <span className="trace-detail-label">Confidence</span>
              <span className="trace-detail-value">{data.confidence ? `${Math.round(data.confidence * 100)}%` : "—"}</span>

              <span className="trace-detail-label">Latency</span>
              <span className="trace-detail-value">{fmtLatency(data.latency_ms)}</span>

              <span className="trace-detail-label">Started</span>
              <span className="trace-detail-value">{absTime(data.started_at || data.created_at)}</span>

              {(data.input_tokens > 0 || data.output_tokens > 0) && (<>
                <span className="trace-detail-label">Tokens in / out</span>
                <span className="trace-detail-value">{fmtTokens(data.input_tokens, data.output_tokens)}</span>

                <span className="trace-detail-label">Cache write / read</span>
                <span className="trace-detail-value">
                  {fmtCacheTokens(data.cache_creation_input_tokens, data.cache_read_input_tokens) ?? "—"}
                </span>

                <span className="trace-detail-label">Est. cost</span>
                <span className="trace-detail-value">{fmtCost(data.estimated_cost_usd)}</span>
              </>)}

              {data.sources?.length > 0 && (<>
                <span className="trace-detail-label">Sources</span>
                <span className="trace-detail-value">{data.sources.join(", ")}</span>
              </>)}

              {data.tool_calls?.length > 0 && (<>
                <span className="trace-detail-label">Tool calls</span>
                <span className="trace-detail-value">{data.tool_calls.join(", ")}</span>
              </>)}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export function TracesPanel() {
  const [intentFilter, setIntentFilter] = useState("");
  const [tierFilter,   setTierFilter]   = useState("");
  const [fromDate,     setFromDate]     = useState("");
  const [toDate,       setToDate]       = useState("");
  const [selectedId,   setSelectedId]   = useState<string | null>(null);

  const { data = [], isLoading, isError, error } = useQuery<TraceRecord[], Error>({
    queryKey: ["traces"],
    queryFn: listTraces,
    refetchInterval: 15_000,
  });

  const intents = [...new Set(data.map(t => t.intent).filter(Boolean))].sort();
  const tiers   = [...new Set(data.map(t => t.tier).filter(Boolean))].sort();

  const rows = data.filter(t => {
    if (intentFilter && t.intent !== intentFilter) return false;
    if (tierFilter   && t.tier   !== tierFilter)   return false;
    if (fromDate) {
      const from = new Date(fromDate).getTime();
      if (new Date(t.started_at || t.created_at).getTime() < from) return false;
    }
    if (toDate) {
      const to = new Date(toDate).getTime() + 86_400_000; // inclusive day
      if (new Date(t.started_at || t.created_at).getTime() > to) return false;
    }
    return true;
  });

  const totals = computeTotals(rows);

  return (
    <>
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Query History</h2>
          <p className="adm-panel__desc">
            Last 200 queries — intent, tier, status, latency, and LLM cost (Tier-2 only).
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
          <input
            type="date"
            value={fromDate}
            onChange={e => setFromDate(e.target.value)}
            title="From date"
            className="adm-date-input"
          />
          <span className="adm-muted">→</span>
          <input
            type="date"
            value={toDate}
            onChange={e => setToDate(e.target.value)}
            title="To date"
            className="adm-date-input"
          />
          {(intentFilter || tierFilter || fromDate || toDate) && (
            <button
              className="adm-btn adm-btn--ghost"
              onClick={() => { setIntentFilter(""); setTierFilter(""); setFromDate(""); setToDate(""); }}
            >
              Clear
            </button>
          )}
        </div>
      </div>

      {/* Cost summary bar — only shown when there are Tier-2 calls in view */}
      {totals.count > 0 && (
        <div className="adm-cost-summary">
          <span className="adm-cost-summary__label">Tier-2 in view:</span>
          <span className="adm-cost-summary__item">
            <strong>{totals.count}</strong> calls
          </span>
          <span className="adm-cost-summary__sep">·</span>
          <span className="adm-cost-summary__item">
            <strong>{(totals.inputTokens / 1000).toFixed(1)}k</strong> in&nbsp;/&nbsp;
            <strong>{(totals.outputTokens / 1000).toFixed(1)}k</strong> out
          </span>
          {(totals.cacheWrite > 0 || totals.cacheRead > 0) && (<>
            <span className="adm-cost-summary__sep">·</span>
            <span className="adm-cost-summary__item adm-muted" title="Prompt cache activity">
              {totals.cacheWrite > 0 && <><strong>{(totals.cacheWrite / 1000).toFixed(1)}k</strong> cache wrote</>}
              {totals.cacheWrite > 0 && totals.cacheRead > 0 && " · "}
              {totals.cacheRead  > 0 && <><strong>{(totals.cacheRead  / 1000).toFixed(1)}k</strong> cache read</>}
            </span>
          </>)}
          <span className="adm-cost-summary__sep">·</span>
          <span className="adm-cost-summary__item">
            est. <strong className="adm-cost-summary__cost">${totals.cost.toFixed(4)}</strong> USD
          </span>
        </div>
      )}

      {isLoading && <p className="adm-loading">Loading…</p>}
      {isError && <p className="adm-error">{error.message}</p>}

      {!isLoading && rows.length === 0 && (
        <div className="adm-empty">No queries match the current filters.</div>
      )}

      {rows.length > 0 && (
        <div className="adm-table-wrap">
          <table className="adm-table adm-table--traces">
            <thead>
              <tr>
                <th>Started</th>
                <th>Duration</th>
                <th>Question</th>
                <th>Intent</th>
                <th>Namespace</th>
                <th>Tier</th>
                <th>Status</th>
                <th>Conf</th>
                <th>In / Out tokens</th>
                <th>Cache tokens</th>
                <th>Cost</th>
                <th>Tools</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(t => {
                const startIso = t.started_at || t.created_at;
                const completedAt = t.latency_ms > 0
                  ? new Date(new Date(startIso).getTime() + t.latency_ms).toLocaleTimeString()
                  : null;
                const cacheInfo = fmtCacheTokens(
                  t.cache_creation_input_tokens ?? 0,
                  t.cache_read_input_tokens ?? 0,
                );
                return (
                  <tr
                    key={t.id}
                    className="adm-row--clickable"
                    onClick={() => setSelectedId(t.id)}
                    title="Click to view full trace"
                  >
                    <td className="adm-muted adm-nowrap" title={absTime(startIso)}>
                      {relTime(startIso)}
                      {completedAt && (
                        <div className="adm-subtext">→ {completedAt}</div>
                      )}
                    </td>
                    <td className="adm-num adm-nowrap">{fmtLatency(t.latency_ms)}</td>
                    <td className="adm-question" title={t.question}>{truncate(t.question, 55)}</td>
                    <td><code className="adm-code">{t.intent}</code></td>
                    <td className="adm-muted">{t.namespace || "—"}</td>
                    <td><span className={TIER_CLS[t.tier] ?? "adm-badge adm-badge--muted"}>{t.tier}</span></td>
                    <td><span className={STATUS_CLS[t.status] ?? "adm-badge adm-badge--muted"}>{t.status}</span></td>
                    <td className="adm-num">{t.confidence ? `${Math.round(t.confidence * 100)}%` : "—"}</td>
                    <td className="adm-num adm-nowrap adm-muted">{fmtTokens(t.input_tokens, t.output_tokens)}</td>
                    <td className="adm-num adm-nowrap adm-muted">{cacheInfo ?? "—"}</td>
                    <td className="adm-num adm-nowrap">{fmtCost(t.estimated_cost_usd)}</td>
                    <td className="adm-muted">{t.tool_calls?.join(", ") || "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
    {selectedId && (
      <TraceDrawer id={selectedId} onClose={() => setSelectedId(null)} />
    )}
    </>
  );
}
