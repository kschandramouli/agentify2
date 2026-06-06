// Typed client for the agentify backend. Calls are same-origin (/api, /admin) and
// proxied to the Go backend by Vite in dev (see vite.config.ts).

export interface QueryResponse {
  answer: string;
  status: string;
  confidence: number; // 0.0–1.0
  sources: string[];
  trace_id?: string;
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

async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export function askQuery(question: string, namespace: string): Promise<QueryResponse> {
  return postJSON<QueryResponse>("/api/query", { question, context: { namespace } });
}

export function listPods(): Promise<Pod[]> {
  return getJSON<Pod[]>("/admin/pods");
}
