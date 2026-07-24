import { useState } from "react";
import { runLiveTool, type ChatMessageDetails, type RecommendedAction } from "../api";

function statusIcon(status: string): string {
  if (status === "healthy") return "✓";
  if (status === "degraded") return "⚠";
  if (["unhealthy", "critical", "error"].includes(status)) return "✗";
  return "–";
}

function statusClass(status: string): string {
  if (status === "healthy") return "ok";
  if (status === "degraded") return "warn";
  if (["unhealthy", "critical", "error"].includes(status)) return "crit";
  return "muted";
}

// ── Collapsible section ──────────────────────────────────────────────────────

function Section({
  title, items, defaultOpen = true,
}: {
  title: string;
  items: string[];
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  if (items.length === 0) return null;
  return (
    <div className="diag-section">
      <button className="diag-section__toggle" type="button" onClick={() => setOpen(o => !o)}>
        {open ? "▾" : "▸"} {title}
      </button>
      {open && (
        <ul className="diag-section__list">
          {items.map((item, i) => <li key={i}>{item}</li>)}
        </ul>
      )}
    </div>
  );
}

// ── Runnable recommended action ──────────────────────────────────────────────

type RunState = "idle" | "running" | "ok" | "error";

function ActionRow({ action }: { action: RecommendedAction }) {
  const [state, setState] = useState<RunState>("idle");
  const [output, setOutput] = useState<string>("");

  async function handleRun() {
    setState("running");
    try {
      const result = await runLiveTool(action.tool, action.arguments);
      const data = result.data as Record<string, unknown>;
      if (data && typeof data.error === "string") {
        setState("error");
        setOutput(data.error);
      } else {
        setState("ok");
        setOutput(JSON.stringify(data, null, 2));
      }
    } catch (e) {
      setState("error");
      setOutput(e instanceof Error ? e.message : "Run failed.");
    }
  }

  return (
    <div className="diag-action">
      <div className="diag-action__row">
        <span className={`diag-action__dot diag-action__dot--${state}`} />
        <span className="diag-action__label">{action.label}</span>
        <button
          className="diag-action__run"
          type="button"
          onClick={handleRun}
          disabled={state === "running"}
        >
          {state === "running" ? "Running…" : "Run"}
        </button>
      </div>
      {output && (
        <pre className={`diag-action__output diag-action__output--${state}`}>{output}</pre>
      )}
    </div>
  );
}

// ── Main report ───────────────────────────────────────────────────────────────

export function DiagnosisReport({ details }: { details: ChatMessageDetails }) {
  const sev = statusClass(details.severity ?? details.status ?? "info");
  const findingsAsText = (details.findings ?? []).map(f =>
    typeof f === "string" ? f : JSON.stringify(f),
  );

  return (
    <div className={`diag-report diag-report--${sev}`}>
      {(details.incident_summary || details.status) && (
        <div className={`diag-banner diag-banner--${sev}`}>
          <span className="diag-banner__icon">{statusIcon(details.status ?? "")}</span>
          <span className="diag-banner__text">{details.incident_summary || details.status}</span>
        </div>
      )}

      <Section title="What happened" items={details.timeline ?? []} />
      <Section title="Findings" items={findingsAsText} />

      {details.likely_cause && (
        <div className="diag-callout">
          <span className="diag-callout__label">Likely cause</span>
          <span className="diag-callout__text">{details.likely_cause}</span>
        </div>
      )}

      <Section title="Recommendations" items={details.recommendations ?? []} defaultOpen={false} />

      {(details.recommended_actions ?? []).length > 0 && (
        <div className="diag-section">
          <div className="diag-section__title">Recommended actions</div>
          {(details.recommended_actions ?? []).map((a, i) => <ActionRow key={i} action={a} />)}
        </div>
      )}
    </div>
  );
}
