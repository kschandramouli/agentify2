import { useState, type FormEvent } from "react";
import {
  checkHealth, checkCerts, diagnoseService,
  checkChangeHistory, checkRestartTrend,
  type QueryResponse, type ServiceContext,
} from "../api";
import { SearchInput } from "./SearchInput";
import { HealthCard } from "./HealthCard";
import { CertCard } from "./CertCard";

// Statuses that warrant escalating to Tier-2 (Claude Opus).
// "partial" = agent unreachable (Go fallback) — show AgentUnavailableBanner.
// "error"   = agent ran but Claude API failed — show Analysis failed card.
const NEEDS_CLAUDE: Set<string> = new Set(["degraded", "unhealthy", "error", "partial"]);

// ── Input parser ──────────────────────────────────────────────────────────────
// Accepts K8s-style free text in these formats:
//   namespace/service   →  { namespace: "payments", service: "payment" }
//   service             →  { namespace: "",          service: "payment" }
//   namespace/          →  { namespace: "payments",  service: "" }       ← mid-type
//
// DNS names like payment.payments.svc.cluster.local are also accepted:
//   first label = service, second = namespace.
function parseInput(raw: string): ServiceContext | null {
  const s = raw.trim();
  if (!s) return null;

  // DNS format: service.namespace.svc.*
  const dnsParts = s.split(".");
  if (dnsParts.length >= 2 && s.includes(".svc")) {
    return { service: dnsParts[0], namespace: dnsParts[1], dns: s };
  }

  // namespace/service
  const slash = s.indexOf("/");
  if (slash !== -1) {
    return {
      namespace: s.slice(0, slash).trim(),
      service:   s.slice(slash + 1).trim(),
    };
  }

  // bare name — treat as service, namespace inferred by backend
  return { service: s, namespace: "" };
}


// ── Sub-components ────────────────────────────────────────────────────────────

interface CheckResult {
  label: string;
  tier: 1 | 2;
  resp?: QueryResponse;
  error?: string;
  durationMs?: number;
}

function statusToSev(status: string): "ok" | "warn" | "crit" | "muted" {
  if (["healthy", "ok", "not_applicable"].includes(status)) return "ok";
  if (["degraded", "partial", "warning"].includes(status)) return "warn";
  if (["unhealthy", "error", "critical"].includes(status)) return "crit";
  return "muted";
}

function Spinner() {
  return <span className="spinner" aria-label="loading" />;
}

function Badge({ status }: { status: string }) {
  return <span className={`badge badge--${statusToSev(status)}`}>{status}</span>;
}

function TierTag({ tier, ms }: { tier: 1 | 2; ms?: number }) {
  return (
    <span
      className={`tier-tag tier-tag--${tier}`}
      title={tier === 1 ? "Deterministic — no LLM" : "Claude Opus — synthesis"}
    >
      Tier-{tier}{ms !== undefined ? ` · ${ms}ms` : ""}
    </span>
  );
}

function CheckCard({ result }: { result: CheckResult }) {
  const sev = result.resp ? statusToSev(result.resp.status) : (result.error ? "crit" : "muted");
  const icon = sev === "ok" ? "✓" : sev === "warn" ? "⚠" : sev === "crit" ? "✗" : "–";
  return (
    <div className={`check-card check-card--${sev}`}>
      <div className="check-card__header">
        <span className="check-card__icon">{icon}</span>
        <span className="check-card__label">{result.label}</span>
        <TierTag tier={result.tier} ms={result.durationMs} />
        {result.resp && <Badge status={result.resp.status} />}
      </div>
      {result.error && <p className="check-card__answer error">{result.error}</p>}
      {result.resp && <p className="check-card__answer">{result.resp.answer}</p>}
      {result.resp?.trace_id && (
        <p className="check-card__trace">trace: <code>{result.resp.trace_id}</code></p>
      )}
    </div>
  );
}

function AgentUnavailableBanner({ answer }: { answer: string }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="check-card check-card--warn">
      <div className="check-card__header">
        <span className="check-card__icon">⚠</span>
        <span className="check-card__label">Claude agent unavailable</span>
      </div>
      <p className="check-card__answer">
        Data was fetched from the cluster but could not be analysed — the agent
        service is not reachable. Start it with{" "}
        <code>cd src/agent && python main.py</code> and retry.
      </p>
      <button
        type="button"
        className="eval-options__info"
        style={{ marginTop: 6 }}
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? "▾" : "▸"} Show raw data
      </button>
      {expanded && <pre className="check-card__raw">{answer}</pre>}
    </div>
  );
}

function DiagnosisCard({
  resp, durationMs, error,
}: { resp?: QueryResponse; durationMs?: number; error?: string }) {
  if (error) return (
    <div className="diagnosis-card diagnosis-card--crit">
      <h3>Diagnosis failed</h3>
      <p className="error">{error}</p>
    </div>
  );
  if (!resp) return null;
  if (resp.status === "partial") return <AgentUnavailableBanner answer={resp.answer} />;
  if (resp.status === "error") return (
    <div className="diagnosis-card diagnosis-card--crit">
      <div className="diagnosis-card__header">
        <h3>Analysis failed</h3>
        <TierTag tier={2} ms={durationMs} />
      </div>
      <p className="diagnosis-card__answer">{resp.answer}</p>
    </div>
  );
  const d = resp.details ?? {};
  const sev = statusToSev(d.severity ?? resp.status);
  return (
    <div className={`diagnosis-card diagnosis-card--${sev}`}>
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
      {!!d.findings?.length && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">Findings</span>
          <ul>{d.findings.map((f, i) => <li key={i}>{f}</li>)}</ul>
        </div>
      )}
      {!!d.recommendations?.length && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">Recommended actions</span>
          <ol>{d.recommendations.map((r, i) => <li key={i}>{r}</li>)}</ol>
        </div>
      )}
      {/* Tool calls — shows which data Claude fetched to build this answer */}
      {!!resp.tool_calls?.length && (
        <div className="diagnosis-card__section">
          <span className="diagnosis-card__section-label">
            Tools called by Claude ({resp.tool_calls.length})
          </span>
          <div className="tool-calls">
            {resp.tool_calls.map((t, i) => (
              <span key={i} className="tool-call-badge" title={JSON.stringify(t.arguments, null, 2)}>
                {t.name}
              </span>
            ))}
          </div>
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

// ── Tier-2 scenario check card (change history, restart trend) ────────────────
function Tier2CheckCard({ label, description, resp, durationMs, error, pending }: {
  label: string; description: string;
  resp?: QueryResponse; durationMs?: number; error?: string; pending?: boolean;
}) {
  const sev = resp ? statusToSev(resp.status) : (error ? "crit" : "muted");
  return (
    <div className={`check-card check-card--${sev} check-card--tier2`}>
      <div className="check-card__header">
        <span className={`check-card__icon check-card__icon--${sev}`}>
          {pending ? <Spinner /> : sev === "ok" ? "✓" : sev === "muted" ? "–" : "⚠"}
        </span>
        <span className="check-card__label">{label}</span>
        <TierTag tier={2} ms={durationMs} />
        {resp && <Badge status={resp.status} />}
      </div>
      {!resp && !error && !pending && (
        <p className="check-card__answer muted">{description}</p>
      )}
      {error && <p className="check-card__answer error">{error}</p>}
      {resp && <p className="check-card__answer">{resp.answer}</p>}
      {resp?.tool_calls && resp.tool_calls.length > 0 && (
        <div className="tool-calls" style={{ marginTop: 6 }}>
          {resp.tool_calls.map((t, i) => (
            <span key={i} className="tool-call-badge" title={JSON.stringify(t.arguments)}>{t.name}</span>
          ))}
        </div>
      )}
      {resp?.trace_id && <p className="check-card__trace">trace: <code>{resp.trace_id}</code></p>}
    </div>
  );
}

// ── LLM scenarios reference ───────────────────────────────────────────────────
const LLM_SCENARIOS = [
  { tier: 1 as const, label: "Health check",        when: "Any query — always runs first",                cost: "Free" },
  { tier: 1 as const, label: "Certificate check",   when: "Any query — always runs first",                cost: "Free" },
  { tier: 2 as const, label: "Correlated diagnosis", when: "When health or cert is degraded/unhealthy",   cost: "Opus call" },
  { tier: 2 as const, label: "Change history",       when: "Optional — recent deployments & rollouts",   cost: "Opus call" },
  { tier: 2 as const, label: "Restart trend",        when: "Optional — restart counts over time",        cost: "Opus call" },
  { tier: 2 as const, label: "Pod logs",             when: "Claude tool — crash reason from container",  cost: "Opus tool" },
  { tier: 2 as const, label: "Metrics history",      when: "Claude tool — time-series data fetch",       cost: "Opus tool" },
  { tier: 2 as const, label: "Proactive sweep",      when: "Background (P4c) — fires on anomaly detect", cost: "Opus call" },
];

// ── Main component ────────────────────────────────────────────────────────────

interface Tier2Result { resp?: QueryResponse; durationMs?: number; error?: string; pending?: boolean; }

interface EvalState {
  phase: "idle" | "tier1" | "tier2" | "tier2-extra" | "done";
  checks: CheckResult[];
  diagnosis?: QueryResponse;
  diagDurationMs?: number;
  diagError?: string;
  changeHistory?: Tier2Result;
  restartTrend?: Tier2Result;
  ctx?: ServiceContext;
}

export function ServiceEvaluator() {
  const [raw, setRaw] = useState("payments/payment");
  const [state, setState] = useState<EvalState>({ phase: "idle", checks: [] });
  const [runChangeHistory, setRunChangeHistory] = useState(false);
  const [runRestartTrend, setRunRestartTrend] = useState(false);
  const [showScenarios, setShowScenarios] = useState(false);

  const isBusy = state.phase !== "idle" && state.phase !== "done";
  const parsed = parseInput(raw);

  async function onDiagnose(e: FormEvent) {
    e.preventDefault();
    const ctx = parseInput(raw);
    if (!ctx || !ctx.service) return;

    setState({ phase: "tier1", checks: [], ctx });

    // ── Tier-1: health + cert in parallel (no LLM) ──
    const [healthRes, certRes] = await Promise.allSettled([
      timed(() => checkHealth(ctx)),
      timed(() => checkCerts(ctx)),
    ]);

    const checks: CheckResult[] = [
      {
        label: "Service health", tier: 1,
        ...(healthRes.status === "fulfilled"
          ? { resp: healthRes.value.resp, durationMs: healthRes.value.ms }
          : { error: String((healthRes as PromiseRejectedResult).reason?.message ?? "Request failed") }),
      },
      {
        label: "TLS certificates", tier: 1,
        ...(certRes.status === "fulfilled"
          ? { resp: certRes.value.resp, durationMs: certRes.value.ms }
          : { error: String((certRes as PromiseRejectedResult).reason?.message ?? "Request failed") }),
      },
    ];

    const allOk = checks.every(c => !c.error && c.resp && !NEEDS_CLAUDE.has(c.resp.status));
    if (allOk && !runChangeHistory && !runRestartTrend) {
      setState({ phase: "done", checks, ctx });
      return;
    }

    // ── Tier-2: diagnosis (when issues found) + optional extras ──
    setState({ phase: "tier2", checks, ctx,
      changeHistory: runChangeHistory ? { pending: true } : undefined,
      restartTrend: runRestartTrend ? { pending: true } : undefined,
    });

    const promises: Promise<void>[] = [];

    // Correlated diagnosis — only when issues exist
    let diagResult: { diag?: QueryResponse; diagMs?: number; diagErr?: string } = {};
    if (!allOk) {
      const t2 = Date.now();
      promises.push(
        diagnoseService(ctx)
          .then(diag => { diagResult = { diag, diagMs: Date.now() - t2 }; })
          .catch(err => { diagResult = { diagErr: err instanceof Error ? err.message : "failed", diagMs: Date.now() - t2 }; })
      );
    }

    // Optional Tier-2 extras
    let chResult: Tier2Result | undefined;
    let rtResult: Tier2Result | undefined;

    if (runChangeHistory) {
      const t = Date.now();
      promises.push(
        checkChangeHistory(ctx)
          .then(r => { chResult = { resp: r, durationMs: Date.now() - t }; })
          .catch(err => { chResult = { error: err instanceof Error ? err.message : "failed", durationMs: Date.now() - t }; })
      );
    }
    if (runRestartTrend) {
      const t = Date.now();
      promises.push(
        checkRestartTrend(ctx)
          .then(r => { rtResult = { resp: r, durationMs: Date.now() - t }; })
          .catch(err => { rtResult = { error: err instanceof Error ? err.message : "failed", durationMs: Date.now() - t }; })
      );
    }

    await Promise.all(promises);

    setState({
      phase: "done", checks, ctx,
      diagnosis: diagResult.diag,
      diagDurationMs: diagResult.diagMs,
      diagError: diagResult.diagErr,
      changeHistory: chResult,
      restartTrend: rtResult,
    });
  }

  const s = state;

  return (
    <section className="panel evaluator">
      <h2>Service Intelligence</h2>

      <form className="eval-form" onSubmit={onDiagnose}>
        <div className="eval-form__search">
          <SearchInput
            value={raw}
            onChange={setRaw}
            disabled={isBusy}
            placeholder="namespace/service  ·  e.g. payments/payment"
          />
          <button className="eval-form__btn" type="submit" disabled={isBusy || !parsed?.service}>
            {s.phase === "tier1" ? <><Spinner />Checking…</> :
             s.phase === "tier2" || s.phase === "tier2-extra" ? <><Spinner />Diagnosing…</> :
             "Diagnose"}
          </button>
        </div>

        {/* Optional Tier-2 checks */}
        <div className="eval-options">
          <span className="eval-options__label">Also run:</span>
          <label className="eval-option">
            <input type="checkbox" checked={runChangeHistory}
              onChange={e => setRunChangeHistory(e.target.checked)} disabled={isBusy} />
            <span>Recent deployments</span>
            <TierTag tier={2} />
          </label>
          <label className="eval-option">
            <input type="checkbox" checked={runRestartTrend}
              onChange={e => setRunRestartTrend(e.target.checked)} disabled={isBusy} />
            <span>Restart trend</span>
            <TierTag tier={2} />
          </label>
          <button type="button" className="eval-options__info"
            onClick={() => setShowScenarios(v => !v)}>
            {showScenarios ? "▾" : "▸"} When does Claude run?
          </button>
        </div>

        {/* LLM scenarios reference panel */}
        {showScenarios && (
          <div className="scenarios-panel">
            <table className="scenarios-table">
              <thead>
                <tr><th>Scenario</th><th>When triggered</th><th>Cost</th></tr>
              </thead>
              <tbody>
                {LLM_SCENARIOS.map((sc, i) => (
                  <tr key={i}>
                    <td><TierTag tier={sc.tier} />&nbsp;{sc.label}</td>
                    <td className="muted">{sc.when}</td>
                    <td><span className={sc.tier === 1 ? "cost-free" : "cost-llm"}>{sc.cost}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <p className="eval-form__hint">
          Format: <code>namespace/service</code> or <code>service.namespace.svc.cluster.local</code>
          &nbsp;· suggestions load from live tracked services
        </p>
      </form>

      {s.phase !== "idle" && (
        <div className="eval-results">

          {s.checks.length > 0 && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">
                Checks — {s.ctx?.service}{s.ctx?.namespace ? ` · ${s.ctx.namespace}` : ""}
                <TierTag tier={1} />
                <span className="eval-results__no-llm">no LLM</span>
              </h3>
              <div className="check-cards">
                {s.checks.map((c, i) =>
                  c.label === "Service health" && c.resp
                    ? <HealthCard key={i} resp={c.resp} durationMs={c.durationMs} />
                    : c.label === "TLS certificates" && c.resp
                    ? <CertCard key={i} resp={c.resp} durationMs={c.durationMs} />
                    : <CheckCard key={i} result={c} />
                )}
              </div>
            </div>
          )}

          {(s.phase === "tier2" || (s.phase === "done" && !s.diagnosis && !s.diagError && NEEDS_CLAUDE.has(s.checks.find(c => c.resp)?.resp?.status ?? ""))) && !s.diagnosis && !s.diagError && s.phase === "tier2" && (
            <div className="eval-results__diagnosing">
              <Spinner />Issues detected — correlating signals with Claude Opus…
              <TierTag tier={2} />
            </div>
          )}

          {s.phase === "done" && !s.diagnosis && !s.diagError && !runChangeHistory && !runRestartTrend && (
            <div className="eval-results__all-clear">
              <span>✓</span>
              All checks nominal — Claude was not needed.
            </div>
          )}

          {(s.diagnosis || s.diagError) && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">Correlated diagnosis <TierTag tier={2} /></h3>
              <DiagnosisCard resp={s.diagnosis} durationMs={s.diagDurationMs} error={s.diagError} />
            </div>
          )}

          {(s.changeHistory || runChangeHistory) && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">Recent deployments <TierTag tier={2} /></h3>
              <Tier2CheckCard
                label="Change history" pending={s.changeHistory?.pending}
                description="Lists recent deployment rollouts for this service."
                resp={s.changeHistory?.resp} durationMs={s.changeHistory?.durationMs}
                error={s.changeHistory?.error}
              />
            </div>
          )}

          {(s.restartTrend || runRestartTrend) && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">Restart trend <TierTag tier={2} /></h3>
              <Tier2CheckCard
                label="Restart trend" pending={s.restartTrend?.pending}
                description="Shows restart count over time and when climbing started."
                resp={s.restartTrend?.resp} durationMs={s.restartTrend?.durationMs}
                error={s.restartTrend?.error}
              />
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
