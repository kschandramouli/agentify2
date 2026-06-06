# agentify frontend (Ops console)

A lean Vite + React + TypeScript app for the ops console. v1 covers:

- **Ask** — POST `/api/query`, renders the answer with status badge, confidence,
  `sources` (pods consulted), and `trace_id` (provenance, spec 004).
- **Pods** — GET `/admin/pods`, a live table of the pod registry.

> ⚠️ **Not yet built/validated in CI.** This was authored without a local Node
> toolchain (the environment couldn't install one). Run `npm install && npm run build`
> to type-check it before relying on it. It's standard Vite+React+TS, but treat the
> first `npm run build` as the real validation.

## Run (local dev)

Needs the Go backend on `:8080`. Easiest backend (no AWS/Docker):

```sh
# terminal 1 — backend (in-memory registry, no AWS needed)
cd ../backend && REGISTRY_BACKEND=memory PORT=:8080 go run ./cmd/agentify
# (KV/relational queries need Postgres; health "no_data" + /admin/pods work without it.)

# terminal 2 — frontend
npm install
npm run dev    # http://localhost:5173
```

Vite proxies `/api` and `/admin` to `:8080` (see `vite.config.ts`), so there's no
backend CORS to configure for dev.

## Scope / deviations (deliberate, see ROADMAP)

- **No shadcn/Tailwind yet** — CLAUDE.md prescribes them, but visual components add
  little when the result can't be visually validated here; plain CSS for v1, layer
  shadcn when iterating the look with a human.
- **No admin integrations CRUD** — those backend handlers are still stubs.
- **No WebSocket chat** — the backend chat handler is a TODO; v1 uses the REST
  `/api/query` request/response.

## Build for production

```sh
npm run build      # tsc -b && vite build  → dist/
npm run preview     # serve dist/ locally
```

In production, `dist/` is served by a CDN/static host and `/api`+`/admin` are routed
to the backend by the ALB (see ARCHITECTURE.md) — the Vite proxy is dev-only.
