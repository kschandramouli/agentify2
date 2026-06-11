import { useState } from "react";
import {
  Integration,
  IntegrationInput,
  listIntegrations,
  createIntegration,
  updateIntegration,
  deleteIntegration,
} from "../api";

// ── Helpers ───────────────────────────────────────────────────────────────────

async function fetchTrackedNamespaces(): Promise<string[]> {
  const res = await fetch("/admin/tracked");
  if (!res.ok) return [];
  const data = (await res.json()) as string[] | null;
  const entries = data ?? [];
  // Each entry is "namespace/service" — extract unique namespaces.
  const seen = new Set<string>();
  for (const entry of entries) {
    const ns = entry.split("/")[0];
    if (ns) seen.add(ns);
  }
  return [...seen].sort();
}

// ── Status badge ──────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === "active"   ? "int-badge int-badge--active"
    : status === "error"  ? "int-badge int-badge--error"
    : "int-badge int-badge--inactive";
  return <span className={cls}>{status}</span>;
}

// ── Empty state ───────────────────────────────────────────────────────────────

function EmptyState({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="int-empty">
      <p>No integrations configured yet.</p>
      <button className="int-btn int-btn--primary" onClick={onAdd}>
        Add first integration
      </button>
    </div>
  );
}

// ── Integration form (create + edit) ─────────────────────────────────────────

const BLANK: IntegrationInput = { name: "", adapter_url: "", namespaces: [], token: "", status: "inactive" };

function IntegrationForm({
  initial,
  onSave,
  onCancel,
  saving,
}: {
  initial?: Integration;
  onSave: (input: IntegrationInput) => void;
  onCancel: () => void;
  saving: boolean;
}) {
  const [form, setForm] = useState<IntegrationInput>(
    initial
      ? { name: initial.name, adapter_url: initial.adapter_url, namespaces: [], token: "", status: initial.status }
      : BLANK
  );

  function set<K extends keyof IntegrationInput>(k: K, v: IntegrationInput[K]) {
    setForm(f => ({ ...f, [k]: v }));
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    onSave(form);
  }

  return (
    <form className="int-form" onSubmit={handleSubmit}>
      <label className="int-field">
        <span>Name *</span>
        <input
          type="text"
          value={form.name}
          onChange={e => set("name", e.target.value)}
          placeholder="Production cluster"
          required
        />
      </label>
      <label className="int-field">
        <span>Adapter URL *</span>
        <input
          type="url"
          value={form.adapter_url}
          onChange={e => set("adapter_url", e.target.value)}
          placeholder="http://k8fy-adapter:8080"
          required
        />
      </label>
      <label className="int-field">
        <span>Bearer token {initial?.has_token && <em>(leave blank to keep existing)</em>}</span>
        <input
          type="password"
          value={form.token}
          onChange={e => set("token", e.target.value)}
          placeholder={initial?.has_token ? "••••••••" : "optional"}
          autoComplete="new-password"
        />
      </label>
      {initial && (
        <label className="int-field">
          <span>Status</span>
          <select value={form.status} onChange={e => set("status", e.target.value)}>
            <option value="inactive">inactive</option>
            <option value="active">active</option>
            <option value="error">error</option>
          </select>
        </label>
      )}
      <div className="int-form__actions">
        <button type="submit" className="int-btn int-btn--primary" disabled={saving}>
          {saving ? "Saving…" : initial ? "Save changes" : "Create"}
        </button>
        <button type="button" className="int-btn" onClick={onCancel} disabled={saving}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// ── Integration row ───────────────────────────────────────────────────────────

function IntegrationRow({
  integration,
  discoveredNamespaces,
  onEdit,
  onDelete,
}: {
  integration: Integration;
  discoveredNamespaces: string[];
  onEdit: (i: Integration) => void;
  onDelete: (id: string) => void;
}) {
  return (
    <div className="int-row">
      <div className="int-row__main">
        <span className="int-row__name">{integration.name}</span>
        <StatusBadge status={integration.status} />
        {integration.has_token && (
          <span className="int-row__token-dot" title="bearer token configured">🔑</span>
        )}
      </div>
      <div className="int-row__url">{integration.adapter_url}</div>
      <div className="int-row__watching">
        <span className="int-row__watching-label">
          {discoveredNamespaces.length > 0 ? "Watching:" : "No namespaces discovered yet"}
        </span>
        {discoveredNamespaces.map(ns => (
          <code key={ns} className="int-ns-chip">{ns}</code>
        ))}
      </div>
      <div className="int-row__actions">
        <button className="int-btn int-btn--sm" onClick={() => onEdit(integration)}>Edit</button>
        <button className="int-btn int-btn--sm int-btn--danger" onClick={() => onDelete(integration.id)}>Delete</button>
      </div>
    </div>
  );
}

// ── Main panel ────────────────────────────────────────────────────────────────

type Mode = "list" | "create" | { editing: Integration };

export function IntegrationsPanel() {
  const [integrations, setIntegrations] = useState<Integration[] | null>(null);
  const [discoveredNamespaces, setDiscoveredNamespaces] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<Mode>("list");

  // Load integrations + discovered namespaces together on first render.
  if (integrations === null && !loading && !error) {
    setLoading(true);
    Promise.all([listIntegrations(), fetchTrackedNamespaces()])
      .then(([data, ns]) => {
        setIntegrations(data);
        setDiscoveredNamespaces(ns);
        setLoading(false);
      })
      .catch(e => { setError(String(e)); setLoading(false); });
  }

  async function handleSave(input: IntegrationInput) {
    setSaving(true);
    setError(null);
    try {
      if (mode === "create") {
        const created = await createIntegration(input);
        setIntegrations(prev => [...(prev ?? []), created]);
      } else if (typeof mode === "object") {
        const updated = await updateIntegration(mode.editing.id, input);
        setIntegrations(prev => prev?.map(i => i.id === updated.id ? updated : i) ?? []);
      }
      setMode("list");
    } catch (e) {
      setError(`Save failed: ${e}`);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    if (!confirm("Delete this integration? This cannot be undone.")) return;
    setError(null);
    try {
      await deleteIntegration(id);
      setIntegrations(prev => prev?.filter(i => i.id !== id) ?? []);
    } catch (e) {
      setError(`Delete failed: ${e}`);
    }
  }

  const list = integrations ?? [];

  return (
    <div className="int-panel">
      <div className="int-panel__header">
        <h2>Integrations</h2>
        <p className="int-panel__desc">
          Each integration connects agentify to a K8fy adapter.
          Namespaces are discovered automatically from the adapter — no manual configuration needed.
        </p>
        {mode === "list" && (
          <button className="int-btn int-btn--primary" onClick={() => setMode("create")}>
            + Add integration
          </button>
        )}
      </div>

      {error && <div className="int-error">{error}</div>}

      {(mode === "create" || typeof mode === "object") && (
        <div className="int-form-wrap">
          <h3>{mode === "create" ? "New integration" : `Edit: ${(mode as { editing: Integration }).editing.name}`}</h3>
          <IntegrationForm
            initial={typeof mode === "object" ? mode.editing : undefined}
            onSave={handleSave}
            onCancel={() => setMode("list")}
            saving={saving}
          />
        </div>
      )}

      {mode === "list" && (
        <>
          {loading && <p className="int-loading">Loading…</p>}
          {!loading && list.length === 0 && (
            <EmptyState onAdd={() => setMode("create")} />
          )}
          {list.length > 0 && (
            <div className="int-list">
              {list.map(i => (
                <IntegrationRow
                  key={i.id}
                  integration={i}
                  discoveredNamespaces={discoveredNamespaces}
                  onEdit={item => setMode({ editing: item })}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
