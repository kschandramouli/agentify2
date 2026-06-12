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

export interface ToolCall {
  name: string;
  arguments: Record<string, unknown>;
}

export interface QueryResponse {
  answer: string;
  status: string;
  confidence: number; // 0.0–1.0
  sources: string[];
  trace_id?: string;
  tool_calls?: ToolCall[];  // tools Claude called to gather context
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

// Tier-2: what changed recently? (deploy events)
export function checkChangeHistory(ctx: ServiceContext): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", {
    question: `what changed recently for ${ctx.service}? show recent deployments and rollouts`,
    context: { namespace: ctx.namespace, deployment: ctx.service },
  });
}

// Tier-2: restart trend over time
export function checkRestartTrend(ctx: ServiceContext): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", {
    question: `show the restart trend for ${ctx.service} — when did restarts start and how many?`,
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

// ── Integrations ─────────────────────────────────────────────────────────────

export interface Integration {
  id: string;
  name: string;
  adapter_url: string;
  namespaces: string[];
  status: "active" | "inactive" | "error";
  has_token: boolean;
  created_at: string;
  updated_at: string;
}

export interface IntegrationInput {
  name: string;
  adapter_url: string;
  namespaces: string[];
  token?: string;
  status?: string;
}

export function listIntegrations(): Promise<Integration[]> {
  return getJSON<Integration[]>("/admin/integrations");
}

export function getIntegration(id: string): Promise<Integration> {
  return getJSON<Integration>(`/admin/integrations/${id}`);
}

export function createIntegration(input: IntegrationInput): Promise<Integration> {
  return postJSON<Integration>("/admin/integrations", input);
}

export async function updateIntegration(id: string, input: IntegrationInput): Promise<Integration> {
  const res = await fetch(`/admin/integrations/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<Integration>;
}

export async function deleteIntegration(id: string): Promise<void> {
  const res = await fetch(`/admin/integrations/${id}`, { method: "DELETE" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
}

// ── Query History (traces) ────────────────────────────────────────────────────

export interface TraceRecord {
  id: string;
  trace_id: string;
  question: string;
  intent: string;
  namespace: string;
  tier: string;
  status: string;
  confidence: number;
  sources: string[];
  tool_calls: string[];
  latency_ms: number;
  created_at: string;
}

export async function listTraces(): Promise<TraceRecord[]> {
  const res = await fetch("/admin/traces");
  if (res.status === 404) return [];
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<TraceRecord[]>;
}

// ── Metrics summary ───────────────────────────────────────────────────────────

export interface MetricsSummary {
  total_queries: number;
  last_24h_count: number;
  queries_by_tier: Record<string, number>;
  queries_by_status: Record<string, number>;
  queries_by_intent: Record<string, number>;
  avg_agent_latency_ms: number;
  p95_agent_latency_ms: number;
  collected_at: string;
}

export async function getMetricsSummary(): Promise<MetricsSummary> {
  const res = await fetch("/admin/metrics/summary");
  if (res.status === 404) return {
    total_queries: 0, last_24h_count: 0,
    queries_by_tier: {}, queries_by_status: {}, queries_by_intent: {},
    avg_agent_latency_ms: 0, p95_agent_latency_ms: 0,
    collected_at: new Date().toISOString(),
  };
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<MetricsSummary>;
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
