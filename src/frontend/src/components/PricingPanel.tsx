import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { listPricing, upsertPricing, type ModelPricing } from "../api";

function fmt(n: number) {
  return `$${n.toFixed(2)}`;
}

interface EditState {
  model_id: string;
  display_name: string;
  input_per_mtok: string;
  output_per_mtok: string;
  cache_write_per_mtok: string;
  cache_read_per_mtok: string;
}

function toEdit(p: ModelPricing): EditState {
  return {
    model_id: p.model_id,
    display_name: p.display_name,
    input_per_mtok: String(p.input_per_mtok),
    output_per_mtok: String(p.output_per_mtok),
    cache_write_per_mtok: String(p.cache_write_per_mtok),
    cache_read_per_mtok: String(p.cache_read_per_mtok),
  };
}

export function PricingPanel() {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<EditState | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const { data: rows = [], isLoading } = useQuery<ModelPricing[]>({
    queryKey: ["pricing"],
    queryFn: listPricing,
  });

  const mutation = useMutation({
    mutationFn: upsertPricing,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["pricing"] });
      setEditing(null);
      setSaveError(null);
    },
    onError: (e: Error) => setSaveError(e.message),
  });

  function startEdit(p: ModelPricing) {
    setEditing(toEdit(p));
    setSaveError(null);
  }

  function cancelEdit() {
    setEditing(null);
    setSaveError(null);
  }

  function handleSave() {
    if (!editing) return;
    mutation.mutate({
      model_id: editing.model_id,
      display_name: editing.display_name,
      input_per_mtok: parseFloat(editing.input_per_mtok) || 0,
      output_per_mtok: parseFloat(editing.output_per_mtok) || 0,
      cache_write_per_mtok: parseFloat(editing.cache_write_per_mtok) || 0,
      cache_read_per_mtok: parseFloat(editing.cache_read_per_mtok) || 0,
    });
  }

  function field(key: keyof EditState, label: string) {
    if (!editing) return null;
    const isPrice = key !== "model_id" && key !== "display_name";
    return (
      <td className="pricing-td">
        <div className="pricing-edit-cell">
          {isPrice && <span className="pricing-dollar">$</span>}
          <input
            className="pricing-input"
            type={isPrice ? "number" : "text"}
            step={isPrice ? "0.01" : undefined}
            min={isPrice ? "0" : undefined}
            value={editing[key]}
            onChange={e => setEditing({ ...editing, [key]: e.target.value })}
            aria-label={label}
          />
        </div>
      </td>
    );
  }

  if (isLoading) return <p className="muted">Loading pricing…</p>;

  return (
    <div className="pricing-panel">
      <p className="pricing-note muted">
        Indicative retail $/MTok rates — used for cost estimates in Query History and fetched by the agent at startup.
        Editing here updates the database; changes take effect on the agent's next restart.
      </p>

      <div className="pricing-table-wrap">
        <table className="pricing-table">
          <thead>
            <tr>
              <th>Model</th>
              <th>Input</th>
              <th>Output</th>
              <th>Cache Write<br /><span className="muted small">5-min TTL</span></th>
              <th>Cache Read</th>
              <th>Updated</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map(p => {
              const isEditing = editing?.model_id === p.model_id;
              return (
                <tr key={p.model_id} className={isEditing ? "pricing-row pricing-row--editing" : "pricing-row"}>
                  {isEditing ? (
                    <>
                      <td className="pricing-td pricing-td--model">
                        <span className="pricing-model-id">{p.model_id}</span>
                        {field("display_name", "Display name")}
                      </td>
                      {field("input_per_mtok", "Input $/MTok")}
                      {field("output_per_mtok", "Output $/MTok")}
                      {field("cache_write_per_mtok", "Cache write $/MTok")}
                      {field("cache_read_per_mtok", "Cache read $/MTok")}
                      <td className="pricing-td muted small">—</td>
                      <td className="pricing-td pricing-td--actions">
                        <button
                          className="pricing-btn pricing-btn--save"
                          onClick={handleSave}
                          disabled={mutation.isPending}
                        >
                          {mutation.isPending ? "Saving…" : "Save"}
                        </button>
                        <button className="pricing-btn pricing-btn--cancel" onClick={cancelEdit}>
                          Cancel
                        </button>
                      </td>
                    </>
                  ) : (
                    <>
                      <td className="pricing-td pricing-td--model">
                        <span className="pricing-model-id">{p.model_id}</span>
                        <span className="pricing-display-name muted">{p.display_name}</span>
                      </td>
                      <td className="pricing-td pricing-td--num">{fmt(p.input_per_mtok)}</td>
                      <td className="pricing-td pricing-td--num">{fmt(p.output_per_mtok)}</td>
                      <td className="pricing-td pricing-td--num">{fmt(p.cache_write_per_mtok)}</td>
                      <td className="pricing-td pricing-td--num">{fmt(p.cache_read_per_mtok)}</td>
                      <td className="pricing-td muted small">
                        {new Date(p.updated_at).toLocaleDateString()}
                      </td>
                      <td className="pricing-td pricing-td--actions">
                        <button className="pricing-btn pricing-btn--edit" onClick={() => startEdit(p)}>
                          Edit
                        </button>
                      </td>
                    </>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {saveError && <p className="error" style={{ marginTop: 8 }}>Save failed: {saveError}</p>}
    </div>
  );
}
