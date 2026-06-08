import { useState, useId, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { checkHealth, checkCerts, diagnoseService, listPods, type QueryResponse, type ServiceContext } from "../api";

// Statuses that warrant escalating to Tier-2 (Claude Opus)
const NEEDS_CLAUDE: Set<string> = new Set(["degraded", "unhealthy", "error"]);

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

// Extract known namespaces from pod registry (k8fy.live-state.{ns} leaf pods)
function extractNamespaces(pods: { id: string; kind: string }[]): string[] {
  const PREFIX = "k8fy.live-state.";
  return pods
    .filter(p => p.kind === "leaf" && p.id.startsWith(PREFIX))
    .map(p => p.id.slice(PREFIX.length))
    .filter(Boolean);
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
      <div className="diagnosis-card__meta">
        {resp.sources.length > 0 && (
          <span>sources: {resp.sources.map(s => <code key={s}>{s}</code>)}</span>
        )}
        {resp.trace_id && <span>trace: <code>{resp.trace_id}</code></span>}
      </div>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

interface EvalState {
  phase: "idle" | "tier1" | "tier2" | "done";
  checks: CheckResult[];
  diagnosis?: QueryResponse;
  diagDurationMs?: number;
  diagError?: string;
  ctx?: ServiceContext;
}

export function ServiceEvaluator() {
  const listId = useId();
  const [raw, setRaw] = useState("payments/payment");
  const [state, setState] = useState<EvalState>({ phase: "idle", checks: [] });

  // Fetch pod registry to power the autocomplete — refetches every 15s
  const { data: pods } = useQuery({
    queryKey: ["pods"],
    queryFn: listPods,
    refetchInterval: 15_000,
  });
  const namespaces = pods ? extractNamespaces(pods) : [];

  const isBusy = state.phase === "tier1" || state.phase === "tier2";
  const parsed = parseInput(raw);

  async function onDiagnose(e: FormEvent) {
    e.preventDefault();
    const ctx = parseInput(raw);
    if (!ctx || !ctx.service) return;

    setState({ phase: "tier1", checks: [], ctx });

    // ── Tier-1: run health + cert checks in parallel (no LLM) ──
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
          : { error: String((healthRes as PromiseRejectedResult).reason?.message ?? "Request failed") }),
      },
      {
        label: "TLS certificates",
        tier: 1,
        ...(certRes.status === "fulfilled"
          ? { resp: certRes.value.resp, durationMs: certRes.value.ms }
          : { error: String((certRes as PromiseRejectedResult).reason?.message ?? "Request failed") }),
      },
    ];

    // Escalate to Tier-2 only if something is wrong
    const allOk = checks.every(c => !c.error && c.resp && !NEEDS_CLAUDE.has(c.resp.status));
    if (allOk) {
      setState({ phase: "done", checks, ctx });
      return;
    }

    // ── Tier-2: correlated diagnosis via Claude Opus ──
    setState({ phase: "tier2", checks, ctx });
    const t2 = Date.now();
    try {
      const diag = await diagnoseService(ctx);
      setState({ phase: "done", checks, diagnosis: diag, diagDurationMs: Date.now() - t2, ctx });
    } catch (err) {
      setState({
        phase: "done", checks,
        diagError: err instanceof Error ? err.message : "Diagnosis failed",
        diagDurationMs: Date.now() - t2,
        ctx,
      });
    }
  }

  const s = state;

  return (
    <section className="panel evaluator">
      <h2>Service Intelligence</h2>

      <form className="eval-form" onSubmit={onDiagnose}>
        <div className="eval-form__search">
          <div className="eval-form__search-wrap">
            <span className="eval-form__search-icon">⌕</span>
            <input
              className="eval-form__search-input"
              list={listId}
              value={raw}
              onChange={e => setRaw(e.target.value)}
              placeholder="namespace/service  ·  e.g. payments/payment"
              aria-label="Service (namespace/service)"
              disabled={isBusy}
              autoComplete="off"
              required
            />
            {/* Native datalist populated from real-time pod registry */}
            <datalist id={listId}>
              {namespaces.map(ns => (
                <option key={ns} value={`${ns}/`} label={`${ns}/ — namespace`} />
              ))}
            </datalist>
          </div>

          <button className="eval-form__btn" type="submit" disabled={isBusy || !parsed?.service}>
            {s.phase === "tier1" ? <><Spinner />Checking…</> :
             s.phase === "tier2" ? <><Spinner />Diagnosing…</> :
             "Diagnose"}
          </button>
        </div>

        <p className="eval-form__hint">
          Format: <code>namespace/service</code> or <code>service.namespace.svc.cluster.local</code>
          {namespaces.length > 0 && (
            <> · tracked namespaces: {namespaces.map(ns => (
              <button
                key={ns}
                type="button"
                className="eval-form__ns-chip"
                onClick={() => setRaw(v => v.includes("/") ? v : `${ns}/`)}
              >{ns}</button>
            ))}</>
          )}
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
                {s.checks.map((c, i) => <CheckCard key={i} result={c} />)}
              </div>
            </div>
          )}

          {s.phase === "tier2" && (
            <div className="eval-results__diagnosing">
              <Spinner />
              Issues detected — correlating signals with Claude Opus…
              <TierTag tier={2} />
            </div>
          )}

          {s.phase === "done" && !s.diagnosis && !s.diagError && (
            <div className="eval-results__all-clear">
              <span>✓</span>
              All checks nominal — Claude was not needed.
            </div>
          )}

          {(s.diagnosis || s.diagError) && (
            <div className="eval-results__section">
              <h3 className="eval-results__section-title">
                Diagnosis
                <TierTag tier={2} />
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
