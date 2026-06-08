import { useState } from "react";
import type { QueryResponse, PodDetail } from "../api";

function statusIcon(status: string): string {
  if (status === "healthy") return "✓";
  if (status === "degraded") return "⚠";
  if (["unhealthy", "error"].includes(status)) return "✗";
  if (status === "completed") return "◇";
  return "–";
}

function statusClass(status: string): string {
  if (status === "healthy") return "ok";
  if (status === "degraded") return "warn";
  if (["unhealthy", "error"].includes(status)) return "crit";
  return "muted";
}

function RatioBar({ healthy, total }: { healthy: number; total: number }) {
  const pct = total > 0 ? (healthy / total) * 100 : 0;
  const cls = pct === 100 ? "ok" : pct >= 75 ? "warn" : "crit";
  return (
    <div className="ratio-bar">
      <div className={`ratio-bar__fill ratio-bar__fill--${cls}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function PodRow({ pod }: { pod: PodDetail }) {
  const sev = statusClass(pod.status);
  return (
    <tr className={`pod-row pod-row--${sev}`}>
      <td className="pod-row__icon">{statusIcon(pod.status)}</td>
      <td className="pod-row__name">
        <code>{pod.name}</code>
      </td>
      <td>
        <span className={`badge badge--${sev}`}>{pod.status}</span>
      </td>
      <td className="pod-row__reason muted">{pod.reason}</td>
      {!pod.completed && (
        <td className="pod-row__restarts">
          {pod.restarts > 0 ? (
            <span className={pod.restarts >= 3 ? "pod-restarts-warn" : "muted"}>
              {pod.restarts} restart{pod.restarts !== 1 ? "s" : ""}
            </span>
          ) : (
            <span className="muted">—</span>
          )}
        </td>
      )}
      {pod.completed && <td className="muted pod-row__completed-label">old rollout</td>}
    </tr>
  );
}

interface Props {
  resp: QueryResponse;
  durationMs?: number;
}

export function HealthCard({ resp, durationMs }: Props) {
  const d = resp.details;
  const [showCompleted, setShowCompleted] = useState(false);

  // Fallback to plain text if no structured details (e.g. no_data or agent answered)
  if (!d?.pods) {
    return (
      <div className="health-card health-card--plain">
        <p>{resp.answer}</p>
        {resp.trace_id && <p className="muted small">trace: <code>{resp.trace_id}</code></p>}
      </div>
    );
  }

  const activePods   = d.pods.filter(p => !p.completed);
  const completedPods = d.pods.filter(p => p.completed);
  const svcSev = statusClass(d.service_status ?? resp.status);

  return (
    <div className={`health-card health-card--${svcSev}`}>
      {/* ── Summary row ── */}
      <div className="health-card__summary">
        <span className={`health-card__status-icon health-card__status-icon--${svcSev}`}>
          {statusIcon(d.service_status ?? resp.status)}
        </span>
        <div className="health-card__counts">
          <span className="health-card__ratio">
            {d.healthy}<span className="muted">/{d.total_active}</span> healthy
          </span>
          <span className="health-card__pct muted">
            ({((d.ratio ?? 0) * 100).toFixed(0)}%)
          </span>
        </div>
        <RatioBar healthy={d.healthy ?? 0} total={d.total_active ?? 0} />
        {durationMs !== undefined && (
          <span className="health-card__tier tier-tag tier-tag--1">Tier-1 · {durationMs}ms</span>
        )}
      </div>

      {/* ── Active pods table ── */}
      {activePods.length > 0 && (
        <table className="pod-table">
          <thead>
            <tr>
              <th></th>
              <th>Pod</th>
              <th>Status</th>
              <th>Reason</th>
              <th>Restarts</th>
            </tr>
          </thead>
          <tbody>
            {/* Degraded/unhealthy first, then healthy */}
            {[...activePods]
              .sort((a, b) => {
                const ord = (s: string) =>
                  s === "unhealthy" ? 0 : s === "degraded" ? 1 : 2;
                return ord(a.status) - ord(b.status);
              })
              .map(p => <PodRow key={p.name} pod={p} />)}
          </tbody>
        </table>
      )}

      {/* ── Completed pods (collapsed by default) ── */}
      {completedPods.length > 0 && (
        <div className="health-card__completed">
          <button
            className="health-card__completed-toggle"
            type="button"
            onClick={() => setShowCompleted(s => !s)}
          >
            {showCompleted ? "▾" : "▸"} {completedPods.length} completed pod{completedPods.length !== 1 ? "s" : ""} from old rollouts (not counted)
          </button>
          {showCompleted && (
            <table className="pod-table pod-table--dim">
              <tbody>
                {completedPods.map(p => <PodRow key={p.name} pod={p} />)}
              </tbody>
            </table>
          )}
        </div>
      )}

      {resp.trace_id && (
        <p className="muted small health-card__trace">trace: <code>{resp.trace_id}</code></p>
      )}
    </div>
  );
}
