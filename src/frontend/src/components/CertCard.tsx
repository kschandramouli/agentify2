import type { QueryResponse, CertDetail } from "../api";

function formatExpiry(iso: string): string {
  if (!iso) return "—";
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric", month: "short", day: "numeric",
      hour: "2-digit", minute: "2-digit",
    });
  } catch {
    return iso;
  }
}

function DaysBar({ days, threshold }: { days: number; threshold: number }) {
  const pct = Math.min(100, Math.max(0, (days / threshold) * 100));
  const cls = days < 7 ? "crit" : days < threshold ? "warn" : "ok";
  return (
    <div className="days-bar" title={`${days} days until expiry (threshold: ${threshold}d)`}>
      <div className={`days-bar__fill days-bar__fill--${cls}`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function CertRow({ cert, threshold }: { cert: CertDetail; threshold: number }) {
  const sev = cert.urgency;
  const icon = sev === "ok" ? "✓" : sev === "warn" ? "⚠" : "✗";
  return (
    <div className={`cert-row cert-row--${sev}`}>
      <div className="cert-row__header">
        <span className={`cert-row__icon cert-row__icon--${sev}`}>{icon}</span>
        <span className="cert-row__name">
          {cert.namespace && <span className="cert-row__ns muted">{cert.namespace}/</span>}
          <code>{cert.name}</code>
        </span>
        {cert.should_renew && (
          <span className={`badge badge--${sev}`}>renew now</span>
        )}
      </div>

      <div className="cert-row__body">
        <div className="cert-row__days">
          <span className={`cert-row__days-num cert-row__days-num--${sev}`}>
            {cert.days < 0 ? "expired" : `${cert.days}d`}
          </span>
          <span className="muted cert-row__days-label">
            {cert.days < 0 ? " ago" : " until expiry"}
          </span>
          <DaysBar days={Math.max(0, cert.days)} threshold={threshold} />
        </div>
        <div className="cert-row__expiry muted">
          Expires {formatExpiry(cert.expires_at)}
        </div>
      </div>
    </div>
  );
}

interface Props {
  resp: QueryResponse;
  durationMs?: number;
}

export function CertCard({ resp, durationMs }: Props) {
  const d = resp.details;

  if (!d?.certificates) {
    return (
      <div className="check-card check-card--muted">
        <div className="check-card__header">
          <span className="check-card__icon">–</span>
          <span className="check-card__label">TLS certificates</span>
          {durationMs !== undefined && (
            <span className="tier-tag tier-tag--1">Tier-1 · {durationMs}ms</span>
          )}
        </div>
        <p className="check-card__answer">{resp.answer}</p>
      </div>
    );
  }

  const threshold = d.renewal_threshold_days ?? 30;
  const needRenewal = d.certs_needing_renewal ?? 0;
  const total = d.certs_checked ?? 0;
  const overallSev = needRenewal > 0
    ? (d.certificates.some(c => c.urgency === "crit") ? "crit" : "warn")
    : "ok";

  return (
    <div className={`check-card check-card--${overallSev}`}>
      <div className="check-card__header">
        <span className={`check-card__icon check-card__icon--${overallSev}`}>
          {overallSev === "ok" ? "✓" : overallSev === "warn" ? "⚠" : "✗"}
        </span>
        <span className="check-card__label">TLS certificates</span>
        <span className="muted" style={{ fontSize: 13 }}>
          {needRenewal > 0
            ? <><strong style={{ color: overallSev === "crit" ? "#e98080" : "#e3c267" }}>{needRenewal}</strong> of {total} need renewal</>
            : <>{total} certificate{total !== 1 ? "s" : ""} — all valid</>
          }
        </span>
        {durationMs !== undefined && (
          <span className="tier-tag tier-tag--1" style={{ marginLeft: "auto" }}>Tier-1 · {durationMs}ms</span>
        )}
      </div>

      <div className="cert-list">
        {[...d.certificates]
          .sort((a, b) => a.days - b.days)   // most urgent first
          .map(cert => (
            <CertRow key={cert.name} cert={cert} threshold={threshold} />
          ))}
      </div>

      {resp.trace_id && (
        <p className="muted small" style={{ marginTop: 10 }}>trace: <code>{resp.trace_id}</code></p>
      )}
    </div>
  );
}
