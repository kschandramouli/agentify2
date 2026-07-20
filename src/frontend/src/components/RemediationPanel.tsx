import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  listRemediationProposals,
  approveRemediation,
  rejectRemediation,
  type RemediationProposal,
} from "../api";

const STATUS_CLS: Record<string, string> = {
  pending:  "adm-badge adm-badge--warn",
  approved: "adm-badge adm-badge--tier2",
  executed: "adm-badge adm-badge--ok",
  rejected: "adm-badge adm-badge--muted",
  expired:  "adm-badge adm-badge--muted",
  failed:   "adm-badge adm-badge--crit",
};

const ACTION_LABEL: Record<string, string> = {
  restart_deployment:  "Restart deployment",
  scale_deployment:    "Scale deployment",
  rollback_deployment: "Roll back deployment",
  rotate_cert:         "Rotate certificate",
  human_escalation:    "Escalate to human — no automated action available",
};

function relTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return `${Math.round(diff / 1_000)}s ago`;
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)}h ago`;
  return new Date(iso).toLocaleDateString();
}

function actionSummary(p: RemediationProposal): string {
  const parts: string[] = [];
  if (p.action_params?.deployment) parts.push(`deployment=${p.action_params.deployment}`);
  if (p.action_params?.replicas !== undefined) parts.push(`replicas=${p.action_params.replicas}`);
  const label = ACTION_LABEL[p.proposed_action] ?? p.proposed_action;
  return parts.length > 0 ? `${label} (${parts.join(", ")})` : label;
}

function ProposalCard({ p, onDecided }: { p: RemediationProposal; onDecided: () => void }) {
  const [busy, setBusy] = useState<"approve" | "reject" | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const isPending = p.status === "pending";
  const confidence = p.analysis?.confidence;

  async function decide(action: "approve" | "reject") {
    setBusy(action);
    setErr(null);
    try {
      if (action === "approve") await approveRemediation(p.id);
      else await rejectRemediation(p.id);
      onDecided();
    } catch (e) {
      setErr(e instanceof Error ? e.message : `${action} failed`);
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className={`check-card check-card--${p.status === "pending" ? "warn" : p.status === "executed" ? "ok" : p.status === "failed" ? "crit" : "muted"}`}>
      <div className="check-card__header">
        <span className="check-card__label">
          {p.namespace}/{p.service}
        </span>
        <span className="adm-badge adm-badge--muted">{p.use_case === "incident_responder" ? "Incident Responder" : "Deployment Guardian"}</span>
        <span className={STATUS_CLS[p.status] ?? "adm-badge adm-badge--muted"} style={{ marginLeft: "auto" }}>
          {p.status}
        </span>
      </div>

      <p className="check-card__answer">
        <strong>{actionSummary(p)}</strong>
        {confidence !== undefined && (
          <span className="adm-muted"> — confidence {Math.round(confidence * 100)}%</span>
        )}
      </p>

      {p.analysis?.reasoning && (
        <p className="adm-muted small">{p.analysis.reasoning}</p>
      )}
      {p.analysis?.blast_radius && (
        <p className="remediation-blast-radius">⚠ {p.analysis.blast_radius}</p>
      )}
      {p.analysis?.evidence && p.analysis.evidence.length > 0 && (
        <ul className="remediation-evidence">
          {p.analysis.evidence.map((e, i) => <li key={i}>{e}</li>)}
        </ul>
      )}

      <div className="remediation-meta muted small">
        proposed {relTime(p.created_at)}
        {isPending && ` · expires ${relTime(p.expires_at)}`}
        {p.decided_by && ` · decided by ${p.decided_by}`}
        {p.trace_id && <> · trace: <code className="adm-code">{p.trace_id}</code></>}
      </div>

      {p.result && Object.keys(p.result).length > 0 && (
        <details className="remediation-result">
          <summary>Execution result</summary>
          <pre>{JSON.stringify(p.result, null, 2)}</pre>
        </details>
      )}
      {p.error && <p className="adm-error">{p.error}</p>}

      {isPending && (
        <div className="remediation-actions">
          <button
            className="adm-btn adm-btn--primary"
            disabled={busy !== null}
            onClick={() => decide("approve")}
            title="Execute this action now — no further confirmation"
          >
            {busy === "approve" ? "Approving…" : "✓ Approve & execute"}
          </button>
          <button
            className="adm-btn adm-btn--ghost"
            disabled={busy !== null}
            onClick={() => decide("reject")}
          >
            {busy === "reject" ? "Rejecting…" : "✕ Reject"}
          </button>
        </div>
      )}
      {err && <p className="adm-error">{err}</p>}
    </div>
  );
}

export function RemediationPanel() {
  const queryClient = useQueryClient();
  const { data = [], isLoading, isError, error } = useQuery<RemediationProposal[], Error>({
    queryKey: ["remediation-proposals"],
    queryFn: () => listRemediationProposals(),
    refetchInterval: 10_000,
  });

  const pending = data.filter(p => p.status === "pending");
  const history = data
    .filter(p => p.status !== "pending")
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());

  const refresh = () => queryClient.invalidateQueries({ queryKey: ["remediation-proposals"] });

  return (
    <div className="adm-panel">
      <div className="adm-panel__header">
        <div>
          <h2>Remediation</h2>
          <p className="adm-panel__desc">
            Proposed Phase-3 actions (restart / scale / rollback) awaiting explicit approval.
            Nothing here executes automatically, regardless of confidence — see ADR 0020.
          </p>
        </div>
      </div>

      {isLoading && <p className="adm-loading">Loading…</p>}
      {isError && <p className="adm-error">{error.message}</p>}

      {!isLoading && pending.length === 0 && (
        <div className="adm-empty">No pending remediation proposals.</div>
      )}

      {pending.length > 0 && (
        <div className="remediation-list">
          {pending.map(p => <ProposalCard key={p.id} p={p} onDecided={refresh} />)}
        </div>
      )}

      {history.length > 0 && (
        <details className="remediation-history">
          <summary>History ({history.length})</summary>
          <div className="remediation-list">
            {history.map(p => <ProposalCard key={p.id} p={p} onDecided={refresh} />)}
          </div>
        </details>
      )}
    </div>
  );
}
