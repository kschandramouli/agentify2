import { useState, type FormEvent } from "react";
import { checkHealth, checkCerts, diagnoseService, type QueryResponse, type ServiceContext } from "../api";

// A check that needs Claude synthesis to answer
const NEEDS_CLAUDE: Set<string> = new Set(["degraded", "unhealthy", "error"]);

interface CheckResult {
  label: string;
  tier: 1 | 2;
  resp?: QueryResponse;
  error?: string;
  durationMs?: number;
}

interface EvalState {
  phase: "idle" | "tier1" | "tier2" | "done" | "error";
  checks: CheckResult[];
  diagnosis?: QueryResponse;
  diagDurationMs?: number;
  diagError?: string;
  ctx?: ServiceContext;
}

function statusToSeverity(status: string): "ok" | "warn" | "crit" | "muted" {
  if (["healthy", "ok", "not_applicable"].includes(status)) return "ok";
  if (["degraded", "partial", "warning"].includes(status)) return "warn";
  if (["unhealthy", "error", "critical"].includes(status)) return "crit";
  return "muted";
}

function Spinner() {
  return <span className="spinner" aria-label="loading" />;
}

function Badge({ status }: { status: string }) {
  const sev = statusToSeverity(status);
  return <span className={`badge badge--${sev}`}>{status}</span>;
}

function TierTag({ tier, ms }: { tier: 1 | 2; ms?: number }) {
  return (
    <span className={`tier-tag tier-tag--${tier}`} title={tier === 1 ? "Deterministic — no LLM" : "Claude Opus — synthesis"}>
      Tier-{tier}{ms !== undefined ? ` · ${ms}ms` : ""}
    </span>
  );
}

function CheckCard({ result }: { result: CheckResult }) {
  const resp = result.resp;
  const sev = resp ? statusToSeverity(resp.status) : "muted";
  const icon = sev === "ok" ? "✓" : sev === "warn" ? "⚠" : sev === "crit" ? "✗" : "–";

  return (
    <div className={`check-card check-card--${sev}`}>
      <div className="check-card__header">
        <span className="check-card__icon">{result.error ? "✗" : icon}</span>
        <span className="check-card__label">{result.label}</span>
        <TierTag tier={result.tier} ms={result.durationMs} />
        {resp && <Badge status={resp.status} />}
      </div>
      {result.error && <p className="check-card__answer error">{result.error}</p>}
      {resp && <p className="check-card__answer">{resp.answer}</p>}
      {resp?.trace_id && <p className="check-card__trace">trace: <code>{resp.trace_id}</code></p>}
    </div>
  );
}

function DiagnosisCard({ resp, durationMs, error }: { resp?: QueryResponse; durationMs?: number; error?: string }) {
  if (error) return (
    <div className="diagnosis-card diagnosis-card--error">
      <h3>Diagnosis failed</h3>
      <p className="error">{error}</p>
    </div>
  );
  if (!resp) return null;

  const d = resp.details ?? {};
  const sev = d.severity ?? resp.status;

  return (
    <div className={`diagnosis-card diagnosis-card--${statusToSeverity(sev)}`}>
      <div className="diagnosis-card__header">
        <h3>Diagnosis</h3>
        <TierTag tier={2} ms={durationMs} />
        {d.severity && <Badge status={d.severity} />}
        <span className="answer__confidence">{(resp.confidence * 100).toFixed(0)}% confidence</span>
      </div>

      <p className="diagnosis-card__answer">{resp.answer}</p>

      {d.likely_cause && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">Likely cause</span>
          <p>{d.likely_cause}</p>
        </div>
      )}

      {d.findings && d.findings.length > 0 && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">Findings</span>
          <ul>
            {d.findings.map((f, i) => <li key={i}>{f}</li>)}
          </ul>
        </div>
      )}

      {d.recommendations && d.recommendations.length > 0 && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">Recommended actions</span>
          <ol>
            {d.recommendations.map((r, i) => <li key={i}>{r}</li>)}
          </ol>
        </div>
      )}

      <div className="diagnosis-card__meta">
        {resp.sources.length > 0 && (
          <span>sources: {resp.sources.map(s => <code key={s}>{s}</code>)}</span>
        )}
        {resp.trace_id && <span>trace: <code>{resp.trace_id}</code></span>}
      </div>
    </div>
  );
}

export function ServiceEvaluator() {
  const [service, setService] = useState("payment");
  const [namespace, setNamespace] = useState("payments");
  const [dns, setDns] = useState("");
  const [state, setState] = useState<EvalState>({ phase: "idle", checks: [] });

  async function onEvaluate(e: FormEvent) {
    e.preventDefault();
    const ctx: ServiceContext = { service: service.trim(), namespace: namespace.trim(), dns: dns.trim() || undefined };

    setState({ phase: "tier1", checks: [], ctx });

    // ── Tier-1: run health + cert checks in parallel (no LLM) ──
    const t1Start = Date.now();
    const [healthRes, certRes] = await Promise.allSettled([
      timed(() => checkHealth(ctx)),
      timed(() => checkCerts(ctx)),
    ]);

    const checks: CheckResult[] = [
      {
        label: "Service health",
        tier: 1,
        ...(healthRes.status === "fulfilled"
          ? { resp: healthRes.value.resp, durationMs: healthRes.value.ms }
          : { error: healthRes.reason?.message ?? "Request failed" }),
      },
      {
        label: "TLS certificates",
        tier: 1,
        ...(certRes.status === "fulfilled"
          ? { resp: certRes.value.resp, durationMs: certRes.value.ms }
          : { error: certRes.reason?.message ?? "Request failed" }),
      },
    ];

    // ── Decide: is Tier-2 needed? ──
    const allOk = checks.every(c =>
      !c.error && c.resp && !NEEDS_CLAUDE.has(c.resp.status)
    );

    if (allOk) {
      setState({ phase: "done", checks, ctx });
      return;
    }

    // ── Tier-2: correlated diagnosis via Claude Opus (only when issues found) ──
    setState({ phase: "tier2", checks, ctx });
    const t2Start = Date.now();
    try {
      const diag = await diagnoseService(ctx);
      setState({ phase: "done", checks, diagnosis: diag, diagDurationMs: Date.now() - t2Start, ctx });
    } catch (err) {
      setState({
        phase: "done", checks,
        diagError: err instanceof Error ? err.message : "Diagnosis failed",
        diagDurationMs: Date.now() - t2Start,
        ctx,
      });
    }

    void t1Start; // suppress unused warning
  }

  const s = state;

  return (
    <section className="panel evaluator">
      <h2>Service Evaluator</h2>

      <form className="eval-form" onSubmit={onEvaluate}>
        <div className="eval-form__fields">
          <label className="eval-form__field">
            <span>Service name</span>
            <input
              value={service}
              onChange={e => setService(e.target.value)}
              placeholder="e.g. payment"
              required
              disabled={s.phase === "tier1" || s.phase === "tier2"}
            />
          </label>
          <label className="eval-form__field">
            <span>Namespace</span>
            <input
              value={namespace}
              onChange={e => setNamespace(e.target.value)}
              placeholder="e.g. payments"
              required
              disabled={s.phase === "tier1" || s.phase === "tier2"}
            />
          </label>
          <label className="eval-form__field eval-form__field--optional">
            <span>DNS name <em>(optional)</em></span>
            <input
              value={dns}
              onChange={e => setDns(e.target.value)}
              placeholder="e.g. payment.payments.svc.cluster.local"
              disabled={s.phase === "tier1" || s.phase === "tier2"}
            />
          </label>
        </div>
        <button
          type="submit"
          className="eval-form__btn"
          disabled={s.phase === "tier1" || s.phase === "tier2"}
        >
          {s.phase === "tier1" ? <><Spinner /> Running checks…</> :
           s.phase === "tier2" ? <><Spinner /> Diagnosing with Claude…</> :
           "Evaluate"}
        </button>
      </form>

      {s.phase !== "idle" && (
        <div className="eval-results">

          {/* Tier-1 status */}
          {s.checks.length > 0 && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">
                Deterministic checks
                <span className="tier-tag tier-tag--1">Tier-1 · no LLM</span>
              </h3>
              <div className="check-cards">
                {s.checks.map((c, i) => <CheckCard key={i} result={c} />)}
              </div>
            </div>
          )}

          {/* Tier-2 pending */}
          {s.phase === "tier2" && (
            <div className="eval-results__section eval-results__diagnosing">
              <Spinner />
              <span>Issues detected — correlating signals with Claude Opus…</span>
              <span className="tier-tag tier-tag--2">Tier-2</span>
            </div>
          )}

          {/* All clear */}
          {s.phase === "done" && !s.diagnosis && !s.diagError && (
            <div className="eval-results__all-clear">
              <span className="check-card__icon">✓</span>
              All checks nominal — Claude was not needed.
            </div>
          )}

          {/* Tier-2 diagnosis */}
          {(s.diagnosis || s.diagError) && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">
                Correlated diagnosis
                <span className="tier-tag tier-tag--2">Tier-2 · Claude Opus</span>
              </h3>
              <DiagnosisCard resp={s.diagnosis} durationMs={s.diagDurationMs} error={s.diagError} />
            </div>
          )}
        </div>
      )}
    </section>
  );
}

async function timed<T>(fn: () => Promise<T>): Promise<{ resp: T; ms: number }> {
  const t = Date.now();
  const resp = await fn();
  return { resp, ms: Date.now() - t };
}
