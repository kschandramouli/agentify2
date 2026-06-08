// Typed client for the agentify backend. Calls are same-origin (/api, /admin) and
// proxied to the Go backend by Vite in dev (see vite.config.ts).

export interface CertDetail {
  name: string;
  namespace: string;
  should_renew: boolean;
  days: number;
  expires_at: string;
  reason: string;
  urgency: "ok" | "warn" | "crit";
}

export interface PodDetail {
  name: string;
  status: string;       // healthy | degraded | unhealthy | completed | unknown
  reason: string;
  phase: string;
  ready: boolean;
  restarts: number;
  completed: boolean;   // true = Succeeded/old pod, excluded from health score
}

export interface QueryResponse {
  answer: string;
  status: string;
  confidence: number; // 0.0–1.0
  sources: string[];
  trace_id?: string;
  details?: {
    // Cert check (Tier-1)
    certs_checked?: number;
    certs_needing_renewal?: number;
    renewal_threshold_days?: number;
    certificates?: CertDetail[];
    // Health check (Tier-1)
    healthy?: number;
    total_active?: number;
    total_completed?: number;
    ratio?: number;
    service_status?: string;
    pods?: PodDetail[];
    // Diagnosis (Tier-2)
    severity?: string;
    likely_cause?: string;
    findings?: string[];
    recommendations?: string[];
  };
}

export interface Pod {
  id: string;
  kind: string;
  summary: string;
  namespace: string;
  store_type: string;
  lifecycle: string;
  event_count: number;
  freshness: string;
  tags: string[] | null;
}

export interface ServiceContext {
  service: string;
  namespace: string;
  dns?: string;
}

async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(190_000), // 190s covers Opus (~90s) + headroom
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

// Tier-1: deterministic health check — no LLM, <10ms
export function checkHealth(ctx: ServiceContext): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", {
    question: `is the ${ctx.service} service healthy?`,
    context: { namespace: ctx.namespace, service: ctx.service },
  });
}

// Tier-1: deterministic cert check — no LLM, <10ms.
// Certs are namespace-scoped in K8s; the backend returns all certs in the
// namespace and tier1Cert filters to secrets matching the service name first,
// falling back to all namespace certs. service is passed for name-matching only.
export function checkCerts(ctx: ServiceContext): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", {
    question: `does ${ctx.service} have any certificates expiring soon?`,
    context: { namespace: ctx.namespace, service_hint: ctx.service },
  });
}

// Tier-2: full correlated diagnosis — Claude Opus, fires only when Tier-1 finds issues
export function diagnoseService(ctx: ServiceContext): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", {
    question: `why is ${ctx.service} having issues? diagnose crashes, cert expiry, recent deploys, and restart trends`,
    context: { namespace: ctx.namespace, service: ctx.service },
  });
}

// Legacy free-text query (kept for backward compat)
export function askQuery(question: string, context: Record<string, string>): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", { question, context });
}

export function listPods(): Promise<Pod[]> {
  return getJSON<Pod[]>("/admin/pods");
}

export interface SyncResult {
  namespaces: { namespace: string; services: string[]; service_count: number }[];
  suggestions: string[];
  total: number;
}

// Trigger a live sync from the adapter — discovers all K8s namespaces/services.
export function syncNamespaces(): Promise<SyncResult> {
  return postJSON<SyncResult>("/admin/sync", {});
}
