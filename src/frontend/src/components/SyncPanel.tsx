import { useState } from "react";
import { syncNamespaces, type SyncResult } from "../api";

async function fetchTracked(): Promise<string[]> {
  const res = await fetch("/admin/tracked");
  if (!res.ok) return [];
  return ((await res.json()) as string[] | null) ?? [];
}

function groupByNamespace(entries: string[]): Record<string, string[]> {
  const m: Record<string, string[]> = {};
  for (const entry of entries) {
    const slash = entry.indexOf("/");
    const ns  = slash >= 0 ? entry.slice(0, slash) : entry;
    const svc = slash >= 0 ? entry.slice(slash + 1) : "";
    if (!m[ns]) m[ns] = [];
    if (svc) m[ns].push(svc);
  }
  return m;
}

export function SyncPanel() {
  const [tracked, setTracked]     = useState<string[] | null>(null);
  const [loading, setLoading]     = useState(false);
  const [syncing, setSyncing]     = useState(false);
  const [syncResult, setSyncResult] = useState<SyncResult | null>(null);
  const [error, setError]         = useState<string | null>(null);

  if (tracked === null && !loading) {
    setLoading(true);
    fetchTracked()
      .then(d => { setTracked(d); setLoading(false); })
      .catch(e => { setError(String(e)); setLoading(false); });
  }

  async function handleSync() {
    setSyncing(true);
    setError(null);
    try {
      const result = await syncNamespaces();
      setSyncResult(result);
      const fresh = await fetchTracked();
      setTracked(fresh);
    } catch (e) {
      setError(`Sync failed: ${e}`);
    } finally {
      setSyncing(false);
    }
  }

  const grouped = groupByNamespace(tracked ?? []);
  const nsNames = Object.keys(grouped).sort();

  return (
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Namespace Sync</h2>
          <p className="adm-panel__desc">
            Namespaces and services the adapter has discovered.
            Run Sync to pull the latest state from the K8s cluster.
          </p>
        </div>
        <button
          className={`adm-btn adm-btn--primary${syncing ? " adm-btn--loading" : ""}`}
          onClick={handleSync}
          disabled={syncing}
        >
          {syncing ? "Syncing…" : "↻ Sync now"}
        </button>
      </div>

      {error && <div className="adm-error">{error}</div>}

      {syncResult && (
        <div className="adm-sync-result">
          ✓ Discovered {syncResult.total} namespace{syncResult.total !== 1 ? "s" : ""}
        </div>
      )}

      {loading && <p className="adm-loading">Loading…</p>}

      {!loading && nsNames.length === 0 && (
        <div className="adm-empty">
          No namespaces discovered yet. Make sure the adapter is running and click Sync.
        </div>
      )}

      {nsNames.length > 0 && (
        <div className="adm-ns-list">
          {nsNames.map(ns => (
            <div key={ns} className="adm-ns-row">
              <div className="adm-ns-row__header">
                <span className="adm-ns-row__name">{ns}</span>
                <span className="adm-ns-row__count">{grouped[ns].length} service{grouped[ns].length !== 1 ? "s" : ""}</span>
              </div>
              <div className="adm-ns-row__svcs">
                {grouped[ns].length === 0
                  ? <span className="adm-muted">no services</span>
                  : grouped[ns].sort().map(svc => (
                      <code key={svc} className="adm-svc-chip">{svc}</code>
                    ))
                }
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
